package configs

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bililive-go/bililive-go/src/types"
	"gopkg.in/yaml.v3"
)

// RPC info.
type RPC struct {
	Enable bool   `yaml:"enable"`
	Bind   string `yaml:"bind"`
}

var defaultRPC = RPC{
	Enable: true,
	Bind:   ":8080",
}

func (r *RPC) verify() error {
	if r == nil {
		return nil
	}
	if !r.Enable {
		return nil
	}
	if _, err := net.ResolveTCPAddr("tcp", r.Bind); err != nil {
		return err
	}
	return nil
}

// Feature info.
type Feature struct {
	UseNativeFlvParser         bool `yaml:"use_native_flv_parser"`
	RemoveSymbolOtherCharacter bool `yaml:"remove_symbol_other_character"`
}

// VideoSplitStrategies info.
type VideoSplitStrategies struct {
	OnRoomNameChanged bool          `yaml:"on_room_name_changed"`
	MaxDuration       time.Duration `yaml:"max_duration"`
	MaxFileSize       int           `yaml:"max_file_size"`
}

// On record finished actions.
type OnRecordFinished struct {
	ConvertToMp4          bool   `yaml:"convert_to_mp4"`
	DeleteFlvAfterConvert bool   `yaml:"delete_flv_after_convert"`
	CustomCommandline     string `yaml:"custom_commandline"`
	FixFlvAtFirst         bool   `yaml:"fix_flv_at_first"`
}

type Log struct {
	OutPutFolder string `yaml:"out_put_folder"`
	SaveLastLog  bool   `yaml:"save_last_log"`
	SaveEveryLog bool   `yaml:"save_every_log"`
	// RotateDays 指定按“天”为单位滚动日志时，最多保留的天数（<=0 表示不清理）
	RotateDays int `yaml:"rotate_days"`
}

// 通知服务所需配置
type Notify struct {
	Telegram Telegram `yaml:"telegram"`
	Email    Email    `yaml:"email"`
}

type Telegram struct {
	Enable           bool   `yaml:"enable"`
	WithNotification bool   `yaml:"withNotification"`
	BotToken         string `yaml:"botToken"`
	ChatID           string `yaml:"chatID"`
}

type Email struct {
	Enable         bool   `yaml:"enable"`
	SMTPHost       string `yaml:"smtpHost"`
	SMTPPort       int    `yaml:"smtpPort"`
	SenderEmail    string `yaml:"senderEmail"`
	SenderPassword string `yaml:"senderPassword"`
	RecipientEmail string `yaml:"recipientEmail"`
}

// Config content all config info.
type Config struct {
	File  string `yaml:"-"`
	RPC   RPC    `yaml:"rpc"`
	Debug bool   `yaml:"debug"`
	// 内部版本号：不参与 YAML 序列化，仅用于乐观并发控制
	Version              int64                `yaml:"-"`
	Interval             int                  `yaml:"interval"`
	OutPutPath           string               `yaml:"out_put_path"`
	FfmpegPath           string               `yaml:"ffmpeg_path"`
	Log                  Log                  `yaml:"log"`
	Feature              Feature              `yaml:"feature"`
	LiveRooms            []LiveRoom           `yaml:"live_rooms"`
	OutputTmpl           string               `yaml:"out_put_tmpl"`
	VideoSplitStrategies VideoSplitStrategies `yaml:"video_split_strategies"`
	Cookies              map[string]string    `yaml:"cookies"`
	OnRecordFinished     OnRecordFinished     `yaml:"on_record_finished"`
	TimeoutInUs          int                  `yaml:"timeout_in_us"`
	Notify               Notify               `yaml:"notify"` // 通知服务配置
	AppDataPath          string               `yaml:"app_data_path"`
	// 只读工具目录：如果指定，则优先从该目录查找外部工具（适用于 Docker 镜像内预置工具）
	ReadOnlyToolFolder string `yaml:"read_only_tool_folder"`
	// 可写工具目录：若指定，则外部工具将下载到该目录。
	// 场景：当 OutPutPath/AppDataPath 位于 exfat/ntfs/cifs 等不支持可执行权限的卷上时，可以将此目录单独挂载到 ext4/xfs 卷。
	ToolRootFolder string `yaml:"tool_root_folder"`

	liveRoomIndexCache map[string]int
}

// 使用 atomic.Value 存放当前配置指针，避免并发读写造成 data race
var config atomic.Value // stores *Config

// 单独的 Debug 原子标志，便于高频读取（例如日志、子进程输出过滤）
var currentDebug atomic.Bool

// 序列化所有 Update 操作，避免并发更新造成的丢写问题
var updateMu sync.Mutex

// 当期望版本与实际版本不一致时返回的错误
var ErrConfigVersionConflict = errors.New("config version conflict")

func SetCurrentConfig(cfg *Config) {
	if cfg == nil {
		// 存储 nil 以保持行为一致
		config.Store((*Config)(nil))
		currentDebug.Store(false)
		return
	}
	config.Store(cfg)
	currentDebug.Store(cfg.Debug)
}

func GetCurrentConfig() *Config {
	v := config.Load()
	if v == nil {
		return nil
	}
	return v.(*Config)
}

// IsDebug 提供并发安全、低开销的 Debug 值读取
func IsDebug() bool {
	return currentDebug.Load()
}

// Update 采用“复制-更新-原子替换”模式安全更新全局配置，并持久化到文件。
// 传入的 mutator 只能对函数参数 c 进行修改，不要持有 c 的指针做异步修改。
// 返回更新后的新配置快照。
func Update(mutator func(c *Config) error) (*Config, error) {
	return updateImpl(mutator, true)
}

// UpdateTransient 与 Update 类似，但不进行文件持久化，仅更新内存配置。
func UpdateTransient(mutator func(c *Config) error) (*Config, error) {
	return updateImpl(mutator, false)
}

func updateImpl(mutator func(c *Config) error, persist bool) (*Config, error) {
	updateMu.Lock()
	defer updateMu.Unlock()
	old := GetCurrentConfig()
	// 若当前尚未设置配置，则以默认配置为基础
	var base *Config
	if old == nil {
		base = NewConfig()
	} else {
		base = CloneConfigShallow(old)
	}
	if err := mutator(base); err != nil {
		return nil, err
	}
	// 维护派生字段
	base.RefreshLiveRoomIndexCache()
	// 版本号自增
	if old == nil {
		base.Version = 1
	} else {
		base.Version = old.Version + 1
	}
	newCfg := base

	if persist && newCfg.File != "" {
		if err := newCfg.Marshal(); err != nil {
			// 如果持久化失败，我们选择记录错误但不阻止内存更新
			// 或者返回错误？这里选择返回错误，因为用户期望保存成功。
			return nil, fmt.Errorf("failed to save config: %w", err)
		}
	}

	SetCurrentConfig(newCfg)
	return newCfg, nil
}

// UpdateCAS 使用期望版本进行乐观并发控制，版本不匹配则返回 ErrConfigVersionConflict
// 默认为持久化更新
func UpdateCAS(expectedVersion int64, mutator func(c *Config) error) (*Config, error) {
	return updateCASImpl(expectedVersion, mutator, true)
}

func updateCASImpl(expectedVersion int64, mutator func(c *Config) error, persist bool) (*Config, error) {
	updateMu.Lock()
	defer updateMu.Unlock()
	cur := GetCurrentConfig()
	// 校验版本
	var curVersion int64
	if cur != nil {
		curVersion = cur.Version
	}
	if curVersion != expectedVersion {
		return nil, ErrConfigVersionConflict
	}
	// 克隆并修改
	var base *Config
	if cur == nil {
		base = NewConfig()
	} else {
		base = CloneConfigShallow(cur)
	}
	if err := mutator(base); err != nil {
		return nil, err
	}
	base.RefreshLiveRoomIndexCache()
	base.Version = expectedVersion + 1

	if persist && base.File != "" {
		if err := base.Marshal(); err != nil {
			return nil, fmt.Errorf("failed to save config: %w", err)
		}
	}

	SetCurrentConfig(base)
	return base, nil
}

// UpdateWithRetry 在读取-修改-提交之间做乐观锁重试，避免调用方自行实现重试逻辑
// maxRetries 为最大重试次数（不含首次尝试），backoff 为两次冲突之间的等待时间
// 默认持久化
func UpdateWithRetry(mutator func(c *Config) error, maxRetries int, backoff time.Duration) (*Config, error) {
	return updateWithRetryImpl(mutator, maxRetries, backoff, true)
}

// UpdateWithRetryTransient 同 UpdateWithRetry，但仅更新内存
func UpdateWithRetryTransient(mutator func(c *Config) error, maxRetries int, backoff time.Duration) (*Config, error) {
	return updateWithRetryImpl(mutator, maxRetries, backoff, false)
}

func updateWithRetryImpl(mutator func(c *Config) error, maxRetries int, backoff time.Duration, persist bool) (*Config, error) {
	for attempt := 0; ; attempt++ {
		snapshot := GetCurrentConfig()
		var ver int64
		if snapshot != nil {
			ver = snapshot.Version
		}
		cfg, err := updateCASImpl(ver, mutator, persist)
		if err == nil {
			return cfg, nil
		}
		if !errors.Is(err, ErrConfigVersionConflict) {
			return nil, err
		}
		if attempt >= maxRetries {
			return nil, err
		}
		time.Sleep(backoff)
	}
}

// MustUpdate 与 Update 类似，但发生错误时会 panic。
func MustUpdate(mutator func(c *Config)) *Config {
	cfg, err := Update(func(c *Config) error { mutator(c); return nil })
	if err != nil {
		panic(err)
	}
	return cfg
}

// SetDebug 原子更新 Debug 标志。
func SetDebug(v bool) (*Config, error) {
	return UpdateWithRetry(func(c *Config) error { c.Debug = v; return nil }, 3, 10*time.Millisecond)
}

// SetCookie 设置某个 host 的 Cookie。
func SetCookie(host, cookie string) (*Config, error) {
	return UpdateWithRetry(func(c *Config) error {
		if c.Cookies == nil {
			c.Cookies = make(map[string]string)
		}
		c.Cookies[host] = cookie
		return nil
	}, 3, 10*time.Millisecond)
}

// AppendLiveRoom 追加一个 LiveRoom。
func AppendLiveRoom(room LiveRoom) (*Config, error) {
	return UpdateWithRetry(func(c *Config) error {
		c.LiveRooms = append(c.LiveRooms, room)
		return nil
	}, 3, 10*time.Millisecond)
}

// RemoveLiveRoomByUrl 从配置中移除指定 URL 的房间
func RemoveLiveRoomByUrl(url string) (*Config, error) {
	return UpdateWithRetry(func(c *Config) error {
		if len(c.LiveRooms) == 0 {
			return nil
		}
		out := c.LiveRooms[:0]
		for _, r := range c.LiveRooms {
			if r.Url != url {
				out = append(out, r)
			}
		}
		c.LiveRooms = out
		return nil
	}, 3, 10*time.Millisecond)
}

// SetLiveRoomListening 设置指定 URL 的房间监听状态
func SetLiveRoomListening(url string, listening bool) (*Config, error) {
	return UpdateWithRetry(func(c *Config) error {
		if room, err := c.GetLiveRoomByUrl(url); err == nil {
			room.IsListening = listening
		}
		return nil
	}, 3, 10*time.Millisecond)
}

// SetLiveRoomId 设置指定 URL 的房间的 LiveId
// LiveId 不持久化，因此使用 Transient 更新
func SetLiveRoomId(url string, id types.LiveID) (*Config, error) {
	return UpdateWithRetryTransient(func(c *Config) error {
		if room, err := c.GetLiveRoomByUrl(url); err == nil {
			room.LiveId = id
		}
		return nil
	}, 3, 10*time.Millisecond)
}

type LiveRoom struct {
	Url         string       `yaml:"url"`
	IsListening bool         `yaml:"is_listening"`
	LiveId      types.LiveID `yaml:"-"`
	Quality     int          `yaml:"quality,omitempty"`
	AudioOnly   bool         `yaml:"audio_only,omitempty"`
	NickName    string       `yaml:"nick_name,omitempty"`
}

type liveRoomAlias LiveRoom

// allow both string and LiveRoom format in config
func (l *LiveRoom) UnmarshalYAML(unmarshal func(any) error) error {
	liveRoomAlias := liveRoomAlias{
		IsListening: true,
	}
	if err := unmarshal(&liveRoomAlias); err != nil {
		var url string
		if err = unmarshal(&url); err != nil {
			return err
		}
		liveRoomAlias.Url = url
	}
	*l = LiveRoom(liveRoomAlias)

	return nil
}

func NewLiveRoomsWithStrings(strings []string) []LiveRoom {
	if len(strings) == 0 {
		return make([]LiveRoom, 0, 4)
	}
	liveRooms := make([]LiveRoom, len(strings))
	for index, url := range strings {
		liveRooms[index].Url = url
		liveRooms[index].IsListening = true
		liveRooms[index].Quality = 0
	}
	return liveRooms
}

var defaultConfig = Config{
	RPC:        defaultRPC,
	Debug:      false,
	Interval:   30,
	OutPutPath: "./",
	FfmpegPath: "",
	Log: Log{
		OutPutFolder: "./",
		SaveLastLog:  true,
		SaveEveryLog: false,
		RotateDays:   7,
	},
	Feature: Feature{
		UseNativeFlvParser:         false,
		RemoveSymbolOtherCharacter: false,
	},
	LiveRooms:          []LiveRoom{},
	File:               "",
	liveRoomIndexCache: map[string]int{},
	VideoSplitStrategies: VideoSplitStrategies{
		OnRoomNameChanged: false,
	},
	OnRecordFinished: OnRecordFinished{
		ConvertToMp4:          false,
		DeleteFlvAfterConvert: false,
		FixFlvAtFirst:         true,
	},
	TimeoutInUs: 60000000,
	Notify: Notify{
		Telegram: Telegram{
			Enable:           false,
			WithNotification: true,
			BotToken:         "",
			ChatID:           "",
		},
		Email: Email{
			Enable:         false,
			SMTPHost:       "smtp.qq.com",
			SMTPPort:       465,
			SenderEmail:    "",
			SenderPassword: "",
			RecipientEmail: "",
		},
	},
	AppDataPath:        "",
	ReadOnlyToolFolder: "",
	ToolRootFolder:     "",
}

func NewConfig() *Config {
	config := defaultConfig
	config.liveRoomIndexCache = map[string]int{}
	newConfigPostProcess(&config)
	return &config
}

func newConfigPostProcess(c *Config) {
	// 若运行在容器内，且未显式指定只读工具目录，则设置为容器内预置目录
	if isInContainer() && strings.TrimSpace(c.ReadOnlyToolFolder) == "" {
		c.ReadOnlyToolFolder = "/opt/bililive/tools"
	}
	if c.AppDataPath == "" {
		c.AppDataPath = filepath.Join(c.OutPutPath, ".appdata")
	}
}

// Verify will return an error when this config has problem.
func (c *Config) Verify() error {
	if c == nil {
		return fmt.Errorf("config is null")
	}
	if err := c.RPC.verify(); err != nil {
		return err
	}
	if c.Interval <= 0 {
		return fmt.Errorf("the interval can not <= 0")
	}
	if _, err := os.Stat(c.OutPutPath); err != nil {
		return fmt.Errorf(`the out put path: "%s" is not exist`, c.OutPutPath)
	}
	if maxDur := c.VideoSplitStrategies.MaxDuration; maxDur > 0 && maxDur < time.Minute {
		return fmt.Errorf("the minimum value of max_duration is one minute")
	}
	if !c.RPC.Enable && len(c.LiveRooms) == 0 {
		return fmt.Errorf("the RPC is not enabled, and no live room is set. the program has nothing to do using this setting")
	}
	return nil
}

// todo remove this function
func (c *Config) RefreshLiveRoomIndexCache() {
	for index, room := range c.LiveRooms {
		c.liveRoomIndexCache[room.Url] = index
	}
}

func (c *Config) RemoveLiveRoomByUrl(url string) error {
	c.RefreshLiveRoomIndexCache()
	if index, ok := c.liveRoomIndexCache[url]; ok {
		if index >= 0 && index < len(c.LiveRooms) && c.LiveRooms[index].Url == url {
			c.LiveRooms = append(c.LiveRooms[:index], c.LiveRooms[index+1:]...)
			delete(c.liveRoomIndexCache, url)
			return nil
		}
	}
	return errors.New("failed removing room: " + url)
}

func (c *Config) GetLiveRoomByUrl(url string) (*LiveRoom, error) {
	room, err := c.getLiveRoomByUrlImpl(url)
	if err != nil {
		c.RefreshLiveRoomIndexCache()
		if room, err = c.getLiveRoomByUrlImpl(url); err != nil {
			return nil, err
		}
	}
	return room, nil
}

func (c Config) getLiveRoomByUrlImpl(url string) (*LiveRoom, error) {
	if index, ok := c.liveRoomIndexCache[url]; ok {
		if index >= 0 && index < len(c.LiveRooms) && c.LiveRooms[index].Url == url {
			return &c.LiveRooms[index], nil
		}
	}
	return nil, errors.New("room " + url + " doesn't exist.")
}

func NewConfigWithBytes(b []byte) (*Config, error) {
	config := defaultConfig
	if err := yaml.Unmarshal(b, &config); err != nil {
		return nil, err
	}
	config.RefreshLiveRoomIndexCache()
	newConfigPostProcess(&config)
	return &config, nil
}

func NewConfigWithFile(file string) (*Config, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("can`t open file: %s", file)
	}
	config, err := NewConfigWithBytes(b)
	if err != nil {
		return nil, err
	}
	config.File = file
	// 可能会修改配置文件（添加缺失字段等），保存回去
	if err := config.Marshal(); err != nil {
		return nil, err
	}
	return config, nil
}

func (c *Config) Marshal() error {
	if c.File == "" {
		return errors.New("config path not set")
	}

	// 1. 将当前配置结构体序列化为新 Node
	var newNode yaml.Node
	// 我们先序列化为字节，然后反序列化为 Node，因为 yaml.Marshal 返回字节。
	// 另外也可以使用 Encoder，但 Unmarshal 更容易获得干净的 Node 树。
	tempBytes, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(tempBytes, &newNode); err != nil {
		return err
	}

	// 2. 注入硬编码的注释
	DecorateConfigNode(&newNode)

	// 3. 将 Node 序列化回字节
	// 使用 Encoder 以设置缩进为 2 空格
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&newNode); err != nil {
		return err
	}

	return os.WriteFile(c.File, buf.Bytes(), 0644)
}

func (c Config) GetFilePath() (string, error) {
	if c.File == "" {
		return "", errors.New("config path not set")
	}
	return c.File, nil
}

// CloneConfigShallow 返回 Config 的浅克隆，并对常见可变字段做拷贝，便于进行“复制-更新-原子替换”以避免并发数据竞争。
// 注意：该函数不会深拷贝嵌套结构中的所有指针字段，请根据需要扩展。
// Config 结构体中还有其他复杂类型（如 RPC、Log、Feature、VideoSplitStrategies、OnRecordFinished、Notify 等嵌套结构体），
// 这些结构体目前仅包含字符串和基本类型，浅拷贝足够。但如果将来这些结构体中添加了指针或切片字段，需要更新克隆逻辑。
func CloneConfigShallow(src *Config) *Config {
	if src == nil {
		return nil
	}
	cp := *src // 先按值复制（浅拷贝）
	// 切片拷贝
	if src.LiveRooms != nil {
		cp.LiveRooms = make([]LiveRoom, len(src.LiveRooms))
		copy(cp.LiveRooms, src.LiveRooms)
	}
	// map 拷贝
	if src.Cookies != nil {
		cp.Cookies = make(map[string]string, len(src.Cookies))
		for k, v := range src.Cookies {
			cp.Cookies[k] = v
		}
	}
	// liveRoomIndexCache 拷贝，避免刷新索引时影响旧快照
	if src.liveRoomIndexCache != nil {
		cp.liveRoomIndexCache = make(map[string]int, len(src.liveRoomIndexCache))
		for k, v := range src.liveRoomIndexCache {
			cp.liveRoomIndexCache[k] = v
		}
	} else {
		cp.liveRoomIndexCache = map[string]int{}
	}
	return &cp
}
