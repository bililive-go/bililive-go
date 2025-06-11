package flag

import (
	"os"
	"time"

	"github.com/alecthomas/kingpin"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

var (
	app = kingpin.New(consts.AppName, "A command-line live stream save tools.").Version(consts.AppVersion)

	Debug           = app.Flag("debug", "Enable debug mode.").Default("false").Bool()
	Interval        = app.Flag("interval", "Interval of query live status").Default("20").Short('t').Int()
	Output          = app.Flag("output", "Output file path.").Short('o').Default("./").String()
	FfmpegPath      = app.Flag("ffmpeg-path", "Path for FFMPEG (default: find FFMPEG from your environment variable)").Default("").String()
	Input           = app.Flag("input", "Live room urls").Short('i').Strings()
	Conf            = app.Flag("config", "Config file.").Short('c').String()
	RPC             = app.Flag("enable-rpc", "Enable RPC server.").Default("false").Bool()
	RPCBind         = app.Flag("rpc-bind", "RPC server bind address").Default(":8080").String()
	NativeFlvParser = app.Flag("native-flv-parser", "use native flv parser").Default("false").Bool()
	OutputFileTmpl  = app.Flag("output-file-tmpl", "output file name template").Default("").String()
	SplitStrategies = app.Flag("split-strategies", "video split strategies, support\"on_room_name_changed\", \"max_duration:(duration)\"").Strings()
)

func init() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
}

// GenConfigFromFlags generates configuration by parsing command line parameters.
func GenConfigFromFlags() *configs.Config {
	cfg := configs.NewConfig()
	cfg.RPC = configs.RPC{
		Enable: *RPC,
		Bind:   *RPCBind,
	}
	cfg.Debug = *Debug
	cfg.Interval = *Interval
	cfg.OutPutPath = *Output
	cfg.FfmpegPath = *FfmpegPath
	cfg.OutputTmpl = *OutputFileTmpl
	cfg.LiveRooms = configs.NewLiveRoomsWithStrings(*Input)
	cfg.Feature = configs.Feature{
		UseNativeFlvParser: *NativeFlvParser,
	}

	if SplitStrategies != nil && len(*SplitStrategies) > 0 {
		for _, s := range *SplitStrategies {
			// TODO: not hard code
			if s == "on_room_name_changed" {
				cfg.VideoSplitStrategies.OnRoomNameChanged = true
			}
			if durStr := utils.Match1(`max_duration:(.*)`, s); durStr != "" {
				dur, err := time.ParseDuration(durStr)
				if err == nil {
					cfg.VideoSplitStrategies.MaxDuration = dur
				}
			}
		}
	}
	return cfg
}
