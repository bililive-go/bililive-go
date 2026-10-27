package recorders

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/pkg/parser"
	"github.com/bililive-go/bililive-go/src/types"
	"github.com/bluele/gcache"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type failingAudioParser func(context.Context, *live.StreamUrlInfo, live.Live, string) error

func (f failingAudioParser) ParseLiveStream(ctx context.Context, s *live.StreamUrlInfo, l live.Live, file string) error {
	return f(ctx, s, l, file)
}
func (f failingAudioParser) Stop() error { return nil }

func TestAudioFailureRetriesVideoAndStillOutputsAAC(t *testing.T) {
	oldConfig := configs.GetCurrentConfig()
	cfg := configs.NewConfig()
	cfg.OutPutPath = t.TempDir()
	cfg.OutputTmpl = "audio.flv"
	cfg.FfmpegPath, _ = os.Executable()
	cfg.Feature.DownloaderType = configs.DownloaderNative
	cfg.Feature.EnableFlvProxySegment = true
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() { configs.SetCurrentConfig(oldConfig) })
	ctrl := gomock.NewController(t)
	l := livemock.NewMockLive(ctrl)
	logger := livelogger.New(0, logrus.Fields{"test": t.Name()})
	l.EXPECT().GetRawUrl().Return("https://live.douyin.com/123").AnyTimes()
	l.EXPECT().GetPlatformCNName().Return("抖音").AnyTimes()
	l.EXPECT().GetLiveId().Return(types.LiveID("audio-test")).AnyTimes()
	l.EXPECT().GetLogger().Return(logger).AnyTimes()
	audio, _ := url.Parse("https://cdn.example/audio.aac?only_audio=1")
	video, _ := url.Parse("https://cdn.example/video.bin")
	stream := &live.StreamUrlInfo{Url: audio, AudioFallbackURL: video}
	l.EXPECT().GetStreamInfos().Return([]*live.StreamUrlInfo{stream}, nil).AnyTimes()
	inst := &instance.Instance{Cache: gcache.New(1).LRU().Build()}
	ctx := context.WithValue(context.Background(), instance.Key, inst)
	events.NewDispatcher(ctx)
	require.NoError(t, inst.Cache.Set(l, &live.Info{Live: l, AudioOnly: true}))
	rec, err := NewRecorder(ctx, l)
	require.NoError(t, err)
	r := rec.(*recorder)
	oldParser := newParser
	t.Cleanup(func() { newParser = oldParser })
	seen := []string{}
	newParser = func(u *url.URL, downloader configs.DownloaderType, options map[string]string, log *livelogger.LiveLogger) (parser.Parser, error) {
		require.Equal(t, configs.DownloaderFFmpeg, downloader)
		require.Equal(t, "true", options["audio_only"])
		require.Empty(t, options["use_flv_proxy"])
		return failingAudioParser(func(ctx context.Context, s *live.StreamUrlInfo, l live.Live, file string) error {
			seen = append(seen, s.Url.String())
			require.Equal(t, ".aac", filepath.Ext(file))
			return fmt.Errorf("模拟媒体不兼容")
		}), nil
	}
	r.tryRecord(ctx)
	require.True(t, time.Now().Before(r.audioFallbackUntil))
	r.tryRecord(ctx)
	require.Equal(t, []string{audio.String(), video.String()}, seen)
	require.Equal(t, audio, stream.Url, "不得改写共享的解析结果")
}
