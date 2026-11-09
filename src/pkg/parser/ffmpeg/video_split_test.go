package ffmpeg

import (
	"github.com/bililive-go/bililive-go/src/configs"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/stretchr/testify/assert"
	gomock "go.uber.org/mock/gomock"
	"testing"
	"time"
)

func TestVideoSplitArgsUsesRoomConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	cfg := configs.NewConfig()
	cfg.VideoSplitStrategies = configs.VideoSplitStrategies{MaxDuration: time.Hour, MaxFileSize: 2 * configs.GB}
	cfg.PlatformConfigs = map[string]configs.PlatformConfig{
		"bilibili": {OverridableConfig: configs.OverridableConfig{VideoSplitStrategies: &configs.VideoSplitStrategies{MaxDuration: 10 * time.Minute, MaxFileSize: configs.GB}}},
	}
	cfg.LiveRooms = []configs.LiveRoom{
		{Url: "https://live.bilibili.com/1"},
		{Url: "https://live.bilibili.com/2", OverridableConfig: configs.OverridableConfig{VideoSplitStrategies: &configs.VideoSplitStrategies{}}},
	}
	previous := configs.GetCurrentConfig()
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })
	for i, room := range cfg.LiveRooms {
		l := livemock.NewMockLive(ctrl)
		l.EXPECT().GetRawUrl().Return(room.Url).AnyTimes()
		args := (&Parser{}).videoSplitArgs(l)
		if i == 0 {
			assert.Equal(t, []string{"-fs", "1073741824"}, args)
		} else {
			assert.Empty(t, args)
		}
	}
}

func TestVideoSplitArgsWithInvalidSizeAndNoLogger(t *testing.T) {
	ctrl := gomock.NewController(t)
	cfg := configs.NewConfig()
	cfg.VideoSplitStrategies.MaxFileSize = -1
	previous := configs.GetCurrentConfig()
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })
	room := livemock.NewMockLive(ctrl)
	room.EXPECT().GetRawUrl().Return("https://live.bilibili.com/1")
	assert.NotPanics(t, func() { assert.Empty(t, (&Parser{}).videoSplitArgs(room)) })
}
