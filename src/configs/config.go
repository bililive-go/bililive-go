package configs

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bililive-go/bililive-go/src/pkg/ratelimit"
	"github.com/bililive-go/bililive-go/src/types"
	"gopkg.in/yaml.v3"
)

// RPC info.
type RPC struct {
	Enable bool   `yaml:"enable" json:"enable"`
	Bind   string `yaml:"bind" json:"bind"`
	// SSE 配置
	SSEListThreshold int `yaml:"sse_list_threshold" json:"sse_list_threshold"` // 监控列表超过此阈值时仅为详情页启用SSE
}

var defaultRPC = RPC{
	Enable:           true,
	Bind:             ":8080",
	SSEListThreshold: 50, // 默认50个直播间
}

func (r *RPC) verify() error {
	if r == nil {
		return nil
	}
	if !r.Enable {
		return nil
	}
	if _, err := net.ResolveTCPAddr("tcp", r.Bind); err != nil {
		return fmt.Errorf("无效的RPC绑定地址: %w", err)
	}
	return nil
}

// Feature info.
type Feature struct {
	// DownloaderType 指定使用的下载器类型
	// 可选值: "ffmpeg" (默认), "native" (内置FLV解析器), "bililive-recorder" (BililiveRecorder CLI)
	DownloaderType DownloaderType `yaml:"downloader_type,omitempty" json:"downloader_type,omitempty"`

	// UseNativeFlvParser 已废弃，保留用于向后兼容
	// 如果设置为 true 且 DownloaderType 为空，则会自动转换为 DownloaderType = "native"
	UseNativeFlvParser         bool `yaml:"use_native_flv_parser,omitempty" json:"use_native_flv_parser,omitempty"`
	RemoveSymbolOtherCharacter bool `yaml:"remove_symbol_other_character" json:"remove_symbol_other_character"`

	// EnableFlvProxySegment 启用 FLV 代理分段功能（仅对 FFmpeg 下载器生效）
	// 当检测到视频编码参数变化（新的 SPS/PPS）时，会主动断开连接触发 FFmpeg 分段
	// 这可以避免因编码参数变化导致的花屏问题
	EnableFlvProxySegment bool `yaml:"enable_flv_proxy_segment,omitempty" json:"enable_flv_proxy_segment,omitempty"`
}

// GetEffectiveDownloaderType 获取实际生效的下载器类型
// 处理向后兼容：如果设置了旧的 UseNativeFlvParser，则转换为对应的 DownloaderType
func (f *Feature) GetEffectiveDownloaderType() DownloaderType {
	if f.DownloaderType != "" && f.DownloaderType.IsValid() {
		return f.DownloaderType
	}
	// 向后兼容：如果设置了 UseNativeFlvParser，转换为 native
	if f.UseNativeFlvParser {
		return DownloaderNative
	}
	return DownloaderFFmpeg
}

// VideoSplitStrategies info.
type VideoSplitStrategies struct {
	OnRoomNameChanged bool          `yaml:"on_room_name_changed" json:"on_room_name_changed"`
	MaxDuration       time.Duration `yaml:"max_duration" json:"max_duration"`
	MaxFileSize       int           `yaml:"max_file_size" json:"max_file_size"`
}

// UploadTiming 上传时机
type UploadTiming string

const (
	// UploadTimingImmediate 录制完成后立即上传原始文件
	UploadTimingImmediate UploadTiming = "immediate"
	// UploadTimingAfterProcess 后处理（修复/转换）完成后上传
	UploadTimingAfterProcess UploadTiming = "after_process"
)

// CloudUpload 云上传配置
type CloudUpload struct {
	Enable             bool     `yaml:"enable" json:"enable"`                                               // 是否启用云上传
	StorageName        string   `yaml:"storage_name" json:"storage_name"`                                   // 使用的 OpenList 存储名称
	UploadPathTmpl     string   `yaml:"upload_path_tmpl" json:"upload_path_tmpl"`                           // 上传路径模板
	DeleteAfterUpload  bool     `yaml:"delete_after_upload" json:"delete_after_upload"`                     // 上传成功后删除本地文件
	AdditionalStorages []string `yaml:"additional_storages,omitempty" json:"additional_storages,omitempty"` // 额外存储（支持多目标上传）
}

// On record finished actions.
type OnRecordFinished struct {
	ConvertToMp4          bool         `yaml:"convert_to_mp4" json:"convert_to_mp4"`
	DeleteFlvAfterConvert bool         `yaml:"delete_flv_after_convert" json:"delete_flv_after_convert"`
	CustomCommandline     string       `yaml:"custom_commandline" json:"custom_commandline"`
	FixFlvAtFirst         bool         `yaml:"fix_flv_at_first" json:"fix_flv_at_first"`
	SaveCover             bool         `yaml:"save_cover" json:"save_cover"`       // 保存视频第一帧作为封面图（.jpg）
	CloudUpload           CloudUpload  `yaml:"cloud_upload" json:"cloud_upload"`   // 云上传配置
	UploadTiming          UploadTiming `yaml:"upload_timing" json:"upload_timing"` // 上传时机
}

type Log struct {
	OutPutFolder string `yaml:"out_put_folder" json:"out_put_folder"`
	SaveLastLog  bool   `yaml:"save_last_log" json:"save_last_log"`
	SaveEveryLog bool   `yaml:"save_every_log" json:"save_every_log"`
	// RotateDays 指定按"天"为单位滚动日志时，最多保留的天数（<=0 表示不清理）
	RotateDays int `yaml:"rotate_days" json:"rotate_days"`
}

// 通知服务所需配置
type Notify struct {
	Telegram Telegram `yaml:"telegram" json:"telegram"`
	Email    Email    `yaml:"email" json:"email"`
	Ntfy     Ntfy     `yaml:"ntfy" json:"ntfy"`
}

type Telegram struct {
	Enable           bool   `yaml:"enable" json:"enable"`
	WithNotification bool   `yaml:"withNotification" json:"withNotification"`
	BotToken         string `yaml:"botToken" json:"botToken"`
	ChatID           string `yaml:"chatID" json:"chatID"`
}

type Email struct {
	Enable         bool   `yaml:"enable" json:"enable"`
	SMTPHost       string `yaml:"smtpHost" json:"smtpHost"`
	SMTPPort       int    `yaml:"smtpPort" json:"smtpPort"`
	SenderEmail    string `yaml:"senderEmail" json:"senderEmail"`
	SenderPassword string `yaml:"senderPassword" json:"senderPassword"`
	RecipientEmail string `yaml:"recipientEmail" json:"recipientEmail"`
}

// TaskQueue 任务队列配置
type TaskQueue struct {
	MaxConcurrent int `yaml:"max_concurrent" json:"max_concurrent"` // 最大并发任务数
}

var defaultTaskQueue = TaskQueue{
	MaxConcurrent: 3,
}

// Sentry 错误监控配置
type Sentry struct {
	Enable bool `yaml:"enable" json:"enable"` // 是否启用 Sentry
}

var defaultSentry = Sentry{
	Enable: false,
}

// Proxy 代理配置
type Proxy struct {
	// Enable 是否启用配置的代理（false 时使用系统环境变量 HTTP_PROXY 等）
	Enable bool `yaml:"enable" json:"enable"`
	// URL 代理地址，支持 http://host:port 或 socks5://host:port
	URL string `yaml:"url" json:"url"`
}

var defaultProxy = Proxy{
	Enable: false,
	URL:    "",
}

// OpenListConfig OpenList 服务配置
type OpenListConfig struct {
	Port     int    `yaml:"port" json:"port"`           // OpenList 监听端口（默认 5244）
	DataPath string `yaml:"data_path" json:"data_path"` // OpenList 数据目录（留空使用默认路径）
}

var defaultOpenListConfig = OpenListConfig{
	Port:     5244,
	DataPath: "", // 默认使用 AppDataPath/openlist
}

// StreamPreference 流偏好配置
// 采用指针模式以区分"未设置"和"设置为零值"
type StreamPreference struct {
	Quality    *string            `yaml:"quality,omitempty" json:"quality,omitempty"`       // 清晰度偏好（如 "1080p", "原画"）
	Attributes *map[string]string `yaml:"attributes,omitempty" json:"attributes,omitempty"` // 平台特定属性（如 format, codec, cdn 等）
}

type ResolvedStreamPreference struct {
	Quality    string            `json:"quality"`
	Attributes map[string]string `json:"attributes"`
}

// OverridableConfig 包含可以在不同层级被覆盖的设置
type OverridableConfig struct {
	Interval             *int                  `yaml:"interval,omitempty" json:"interval,omitempty"`                             // 检测间隔(秒)
	OutPutPath           *string               `yaml:"out_put_path,omitempty" json:"out_put_path,omitempty"`                     // 输出路径
	FfmpegPath           *string               `yaml:"ffmpeg_path,omitempty" json:"ffmpeg_path,omitempty"`                       // FFmpeg可执行文件路径
	Log                  *Log                  `yaml:"log,omitempty" json:"log,omitempty"`                                       // 日志配置
	Feature              *Feature              `yaml:"feature,omitempty" json:"feature,omitempty"`                               // 功能特性配置
	OutputTmpl           *string               `yaml:"out_put_tmpl,omitempty" json:"out_put_tmpl,omitempty"`                     // 输出文件名模板
	VideoSplitStrategies *VideoSplitStrategies `yaml:"video_split_strategies,omitempty" json:"video_split_strategies,omitempty"` // 视频分割策略
	OnRecordFinished     *OnRecordFinished     `yaml:"on_record_finished,omitempty" json:"on_record_finished,omitempty"`         // 录制完成后的动作
	TimeoutInUs          *int                  `yaml:"timeout_in_us,omitempty" json:"timeout_in_us,omitempty"`                   // 超时设置(微秒)
	StreamPreference     *StreamPreference     `yaml:"stream_preference,omitempty" json:"stream_preference,omitempty"`           // 流偏好配置
}

// PlatformConfig 包含平台特定的设置
type PlatformConfig struct {
	OverridableConfig    `yaml:",inline" json:",inline"`
	Name                 string `yaml:"name" json:"name"`                                                           // 平台中文名称
	MinAccessIntervalSec int    `yaml:"min_access_interval_sec,omitempty" json:"min_access_interval_sec,omitempty"` // 平台访问最小间隔(秒)，用于防风控
}

type Ntfy struct {
	Enable bool   `yaml:"enable"`
	URL    string `yaml:"URL"`
	Token  string `yaml:"token"`
	Tag    string `yaml:"tag"`
}

// Config content all config info.
type Config struct {
	// 核心配置
	File    string `yaml:"-" json:"-"`
	RPC     RPC    `yaml:"rpc" json:"rpc"`
	Debug   bool   `yaml:"debug" json:"debug"`
	Version int64  `yaml:"-" json:"-"` // 内部版本号：不参与 YAML/JSON 序列化，仅用于乐观并发控制

	// 全局默认配置（非指针，提供默认值）
	Interval             int                  `yaml:"interval" json:"interval"`
	OutPutPath           string               `yaml:"out_put_path" json:"out_put_path"`
	FfmpegPath           string               `yaml:"ffmpeg_path" json:"ffmpeg_path"`
	Log                  Log                  `yaml:"log" json:"log"`
	Feature              Feature              `yaml:"feature" json:"feature"`
	OutputTmpl           string               `yaml:"out_put_tmpl" json:"out_put_tmpl"`
	VideoSplitStrategies VideoSplitStrategies `yaml:"video_split_strategies" json:"video_split_strategies"`
	OnRecordFinished     OnRecordFinished     `yaml:"on_record_finished" json:"on_record_finished"`
	TimeoutInUs          int                  `yaml:"timeout_in_us" json:"timeout_in_us"`

	// 流偏好配置 - 两套系统并存
	StreamPreference StreamPreference `yaml:"stream_preference,omitempty" json:"stream_preference,omitempty"` // 新版（渐进迁移中）

	// 直播间列表
	LiveRooms []LiveRoom `yaml:"live_rooms" json:"live_rooms"`

	// Cookies 配置
	Cookies map[string]string `yaml:"cookies" json:"cookies"`

	// 通知服务配置
	Notify Notify `yaml:"notify" json:"notify"`

	// 数据目录配置
	AppDataPath        string `yaml:"app_data_path" json:"app_data_path"`
	ReadOnlyToolFolder string `yaml:"read_only_tool_folder" json:"read_only_tool_folder"` // 只读工具目录
	ToolRootFolder     string `yaml:"tool_root_folder" json:"tool_root_folder"`           // 可写工具目录

	// 任务队列配置
	TaskQueue TaskQueue `yaml:"task_queue" json:"task_queue"`

	// Sentry 错误监控配置
	Sentry Sentry `yaml:"sentry" json:"sentry"`

	// 代理配置
	Proxy Proxy `yaml:"proxy" json:"proxy"`

	// OpenList 配置
	OpenList OpenListConfig `yaml:"openlist" json:"openlist"`

	// 平台特定配置（层级覆盖，使用 OverridableConfig 中的指针模式）
	PlatformConfigs map[string]PlatformConfig `yaml:"platform_configs,omitempty" json:"platform_configs,omitempty"`

	// 内部缓存
	liveRoomIndexCache map[string]int `json:"-"`
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
	// 配置更新时同步平台访问频率限制器
	cfg.syncPlatformRateLimits()
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
	Url         string       `yaml:"url" json:"url"`
	IsListening bool         `yaml:"is_listening" json:"is_listening"`
	LiveId      types.LiveID `yaml:"-" json:"live_id,omitempty"`
	Quality     int          `yaml:"quality,omitempty" json:"quality,omitempty"`
	AudioOnly   bool         `yaml:"audio_only,omitempty" json:"audio_only,omitempty"`
	NickName    string       `yaml:"nick_name,omitempty" json:"nick_name,omitempty"`
	SchemeUrl   string       `yaml:"scheme" json:"scheme,omitempty"`

	// 房间级可覆盖配置
	OverridableConfig `yaml:",inline" json:",inline"` // 房间级配置覆盖
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
		SaveCover:             false,
		CloudUpload: CloudUpload{
			Enable:            false,
			StorageName:       "",
			UploadPathTmpl:    "/录播归档/{{ .Platform }}/{{ .HostName }}/{{ .RoomName }}-{{ now | date \"2006-01-02\" }}.{{ .Ext }}",
			DeleteAfterUpload: false,
		},
		UploadTiming: UploadTimingAfterProcess,
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
		Ntfy: Ntfy{
			Enable: false,
			URL:    "",
			Token:  "",
			Tag:    "",
		},
	},
	AppDataPath:        "",
	ReadOnlyToolFolder: "",
	ToolRootFolder:     "",
	TaskQueue:          defaultTaskQueue,
	Sentry:             defaultSentry,
	Proxy:              defaultProxy,
	OpenList:           defaultOpenListConfig,
	PlatformConfigs:    map[string]PlatformConfig{},
}

func NewConfig() *Config {
	config := defaultConfig
	config.liveRoomIndexCache = map[string]int{}
	config.PlatformConfigs = map[string]PlatformConfig{}
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
		return fmt.Errorf("配置不存在")
	}
	if err := c.RPC.verify(); err != nil {
		return err
	}
	if c.Interval <= 0 {
		return fmt.Errorf("检测间隔必须大于 0")
	}
	if _, err := os.Stat(c.OutPutPath); err != nil {
		return fmt.Errorf(`输出路径 "%s" 不存在`, c.OutPutPath)
	}
	if maxDur := c.VideoSplitStrategies.MaxDuration; maxDur > 0 && maxDur < time.Minute {
		return fmt.Errorf("单个视频的最大录制时长最小值为 1 分钟")
	}
	if !c.RPC.Enable && len(c.LiveRooms) == 0 {
		return fmt.Errorf("RPC 服务已禁用且未配置直播间，程序无任务可执行")
	}

	// 验证平台配置
	if err := c.ValidatePlatformConfigs(); err != nil {
		return err
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

	// 确保映射在向后兼容时被初始化
	if config.PlatformConfigs == nil {
		config.PlatformConfigs = map[string]PlatformConfig{}
	}

	config.RefreshLiveRoomIndexCache()
	newConfigPostProcess(&config)
	// 在配置加载时同步平台访问频率限制器
	config.syncPlatformRateLimits()
	return &config, nil
}

func NewConfigWithFile(file string) (*Config, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		// 进行权限诊断，提供更详细的错误信息
		diag := DiagnoseFilePermission(file)
		diagInfo := diag.FormatError()
		if diagInfo != "" {
			return nil, fmt.Errorf("can`t open file: %s%s", file, diagInfo)
		}
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
	// PlatformConfigs 拷贝
	if src.PlatformConfigs != nil {
		cp.PlatformConfigs = make(map[string]PlatformConfig, len(src.PlatformConfigs))
		for k, v := range src.PlatformConfigs {
			cp.PlatformConfigs[k] = v
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

// ResolveConfigForRoom 为指定房间解析最终的配置值
// 通过合并 全局 -> 平台 -> 房间 级别的配置
func (c *Config) ResolveConfigForRoom(room *LiveRoom, platformName string) ResolvedConfig {
	resolved := ResolvedConfig{
		Interval:             c.Interval,
		OutPutPath:           c.OutPutPath,
		FfmpegPath:           c.FfmpegPath,
		Log:                  c.Log,
		Feature:              c.Feature,
		OutputTmpl:           c.OutputTmpl,
		VideoSplitStrategies: c.VideoSplitStrategies,
		OnRecordFinished:     c.OnRecordFinished,
		TimeoutInUs:          c.TimeoutInUs,
	}

	// 应用平台级覆盖
	if platformConfig, exists := c.PlatformConfigs[platformName]; exists {
		resolved.applyOverrides(&platformConfig.OverridableConfig)
	}

	// 应用房间级覆盖
	resolved.applyOverrides(&room.OverridableConfig)

	return resolved
}

// GetPlatformMinAccessInterval 返回指定平台的最小访问间隔
// 强制最小值为 1 秒，不允许无限制高频访问
func (c *Config) GetPlatformMinAccessInterval(platformName string) int {
	minInterval := 1 // 默认最小间隔为 1 秒
	if platformConfig, exists := c.PlatformConfigs[platformName]; exists {
		if platformConfig.MinAccessIntervalSec >= 1 {
			return platformConfig.MinAccessIntervalSec
		}
	}
	return minInterval
}

// syncPlatformRateLimits 同步平台访问频率限制到全局限制器
func (c *Config) syncPlatformRateLimits() {
	rateLimiter := ratelimit.GetGlobalRateLimiter()

	// 清除已有限制
	currentLimits := rateLimiter.GetAllPlatformLimits()

	// 设置新的平台限制
	for platformKey, platformConfig := range c.PlatformConfigs {
		if platformConfig.MinAccessIntervalSec > 0 {
			rateLimiter.SetPlatformLimit(platformKey, platformConfig.MinAccessIntervalSec)
		}
		// 从当前限制列表中移除此平台（标记为已处理）
		delete(currentLimits, platformKey)
	}

	// 清除配置中不再存在的平台限制
	for platformKey := range currentLimits {
		rateLimiter.RemovePlatformLimit(platformKey)
	}
}

// ResolvedConfig 包含房间的最终解析配置值
type ResolvedConfig struct {
	Interval             int                  `json:"interval"`
	OutPutPath           string               `json:"out_put_path"`
	FfmpegPath           string               `json:"ffmpeg_path"`
	Log                  Log                  `json:"log"`
	Feature              Feature              `json:"feature"`
	OutputTmpl           string               `json:"out_put_tmpl"`
	VideoSplitStrategies VideoSplitStrategies `json:"video_split_strategies"`
	OnRecordFinished     OnRecordFinished     `json:"on_record_finished"`
	TimeoutInUs          int                  `json:"timeout_in_us"`
	StreamPreference     StreamPreference     `json:"stream_preference"`
}

// applyOverrides 将可覆盖配置中的非空值应用到解析配置中
func (r *ResolvedConfig) applyOverrides(override *OverridableConfig) {
	if override.Interval != nil {
		r.Interval = *override.Interval
	}
	if override.OutPutPath != nil {
		r.OutPutPath = *override.OutPutPath
	}
	if override.FfmpegPath != nil {
		r.FfmpegPath = *override.FfmpegPath
	}
	if override.Log != nil {
		r.Log = *override.Log
	}
	if override.Feature != nil {
		r.Feature = *override.Feature
	}
	if override.OutputTmpl != nil {
		r.OutputTmpl = *override.OutputTmpl
	}
	if override.VideoSplitStrategies != nil {
		r.VideoSplitStrategies = *override.VideoSplitStrategies
	}
	if override.OnRecordFinished != nil {
		r.OnRecordFinished = *override.OnRecordFinished
	}
	if override.TimeoutInUs != nil {
		r.TimeoutInUs = *override.TimeoutInUs
	}
	if override.StreamPreference != nil {
		r.StreamPreference = *MergeStreamPreference(&r.StreamPreference, override.StreamPreference)
	}
}

// GetPlatformKeyFromUrl 从URL中提取平台键，用于配置查找
func GetPlatformKeyFromUrl(urlStr string) string {
	u, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}

	// 将域名映射到一致的平台键
	domainToPlatformMap := map[string]string{
		"live.bilibili.com":   "bilibili",
		"live.douyin.com":     "douyin",
		"v.douyin.com":        "douyin",
		"www.douyu.com":       "douyu",
		"www.huya.com":        "huya",
		"live.kuaishou.com":   "kuaishou",
		"www.yy.com":          "yy",
		"live.acfun.cn":       "acfun",
		"www.lang.live":       "lang",
		"fm.missevan.com":     "missevan",
		"www.openrec.tv":      "openrec",
		"weibo.com":           "weibolive",
		"live.weibo.com":      "weibolive",
		"www.xiaohongshu.com": "xiaohongshu",
		"xhslink.com":         "xiaohongshu",
		"www.yizhibo.com":     "yizhibo",
		"www.hongdoufm.com":   "hongdoufm",
		"live.kilakila.cn":    "hongdoufm",
		"www.zhanqi.tv":       "zhanqi",
		"cc.163.com":          "cc",
		"www.twitch.tv":       "twitch",
		"egame.qq.com":        "qq",
		"www.huajiao.com":     "huajiao",
	}

	if platform, exists := domainToPlatformMap[u.Host]; exists {
		return platform
	}

	// 备用方案：使用主机名
	return u.Host
}

// GetEffectiveConfigForRoom 返回房间的有效配置
func (c *Config) GetEffectiveConfigForRoom(roomUrl string) ResolvedConfig {
	platformKey := GetPlatformKeyFromUrl(roomUrl)
	room, err := c.GetLiveRoomByUrl(roomUrl)
	if err != nil {
		// 如果未找到房间，创建最小房间用于解析
		room = &LiveRoom{Url: roomUrl}
	}
	return c.ResolveConfigForRoom(room, platformKey)
}

// ValidatePlatformConfigs 验证平台配置的一致性
func (c *Config) ValidatePlatformConfigs() error {
	for platformKey, platformConfig := range c.PlatformConfigs {
		// 验证间隔值
		if platformConfig.Interval != nil && *platformConfig.Interval <= 0 {
			return fmt.Errorf("平台 '%s': 检测间隔必须大于 0", platformKey)
		}

		// 验证最小访问间隔
		if platformConfig.MinAccessIntervalSec < 0 {
			return fmt.Errorf("平台 '%s': 最小访问间隔不能为负数", platformKey)
		}

		// 验证路径（如果指定）
		if platformConfig.OutPutPath != nil {
			if _, err := os.Stat(*platformConfig.OutPutPath); os.IsNotExist(err) {
				return fmt.Errorf("平台 '%s': 输出路径 '%s' 不存在", platformKey, *platformConfig.OutPutPath)
			}
		}
	}
	return nil
}
