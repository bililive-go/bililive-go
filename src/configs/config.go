package configs

import (
	"errors"
	"fmt"
	"net"
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
	// 通知服务配置
	Notify Notify `yaml:"notify"`

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
}

func NewConfig() *Config {
	config := defaultConfig
	config.liveRoomIndexCache = map[string]int{}
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
