package configs

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/bililive-go/bililive-go/src/types"
	"gopkg.in/yaml.v2"
)

// RPC info.
type RPC struct {
	Enable bool   `yaml:"enable"`
	Bind   string `yaml:"bind"`
}

var defaultRPC = RPC{
	Enable: true,
	Bind:   "127.0.0.1:8080",
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

// OverridableConfig 包含可以在不同层级被覆盖的设置
type OverridableConfig struct {
	Interval             *int                  `yaml:"interval,omitempty"`               // 检测间隔(秒)
	OutPutPath           *string               `yaml:"out_put_path,omitempty"`           // 输出路径
	FfmpegPath           *string               `yaml:"ffmpeg_path,omitempty"`            // FFmpeg可执行文件路径
	Log                  *Log                  `yaml:"log,omitempty"`                    // 日志配置
	Feature              *Feature              `yaml:"feature,omitempty"`                // 功能特性配置
	OutputTmpl           *string               `yaml:"out_put_tmpl,omitempty"`           // 输出文件名模板
	VideoSplitStrategies *VideoSplitStrategies `yaml:"video_split_strategies,omitempty"` // 视频分割策略
	OnRecordFinished     *OnRecordFinished     `yaml:"on_record_finished,omitempty"`     // 录制完成后的动作
	TimeoutInUs          *int                  `yaml:"timeout_in_us,omitempty"`          // 超时设置(微秒)
}

// PlatformConfig 包含平台特定的设置
type PlatformConfig struct {
	OverridableConfig    `yaml:",inline"`
	Name                 string `yaml:"name"`                              // 平台中文名称
	MinAccessIntervalSec int    `yaml:"min_access_interval_sec,omitempty"` // 平台访问最小间隔(秒)，用于防风控
}

// Config content all config info.
type Config struct {
	File                 string               `yaml:"-"`
	RPC                  RPC                  `yaml:"rpc"`
	Debug                bool                 `yaml:"debug"`
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

	// 新的层级配置字段
	PlatformConfigs map[string]PlatformConfig `yaml:"platform_configs,omitempty"` // 平台特定配置

	liveRoomIndexCache map[string]int
}

var config *Config

func SetCurrentConfig(cfg *Config) {
	config = cfg
}

func GetCurrentConfig() *Config {
	return config
}

type LiveRoom struct {
	Url         string       `yaml:"url"`
	IsListening bool         `yaml:"is_listening"`
	LiveId      types.LiveID `yaml:"-"`
	Quality     int          `yaml:"quality,omitempty"`
	AudioOnly   bool         `yaml:"audio_only,omitempty"`
	NickName    string       `yaml:"nick_name,omitempty"`

	// 房间级可覆盖配置
	OverridableConfig `yaml:",inline"` // 房间级配置覆盖
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
	PlatformConfigs: map[string]PlatformConfig{},
	AppDataPath:     "./.appdata/",
}

func NewConfig() *Config {
	config := defaultConfig
	config.liveRoomIndexCache = map[string]int{}
	config.PlatformConfigs = map[string]PlatformConfig{}
	return &config
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
	return config, nil
}

func (c *Config) Marshal() error {
	if c.File == "" {
		return errors.New("config path not set")
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(c.File, b, os.ModeAppend)
}

func (c Config) GetFilePath() (string, error) {
	if c.File == "" {
		return "", errors.New("config path not set")
	}
	return c.File, nil
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
func (c *Config) GetPlatformMinAccessInterval(platformName string) int {
	if platformConfig, exists := c.PlatformConfigs[platformName]; exists {
		return platformConfig.MinAccessIntervalSec
	}
	return 0 // 未指定时无限制
}

// ResolvedConfig 包含房间的最终解析配置值
type ResolvedConfig struct {
	Interval             int
	OutPutPath           string
	FfmpegPath           string
	Log                  Log
	Feature              Feature
	OutputTmpl           string
	VideoSplitStrategies VideoSplitStrategies
	OnRecordFinished     OnRecordFinished
	TimeoutInUs          int
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
