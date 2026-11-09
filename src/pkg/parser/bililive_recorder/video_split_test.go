package bililive_recorder

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
		args := videoSplitArgs(l)
		if i == 0 {
			assert.Equal(t, []string{"--max-size", "1024.00", "--max-duration", "10.00"}, args)
		} else {
			assert.Empty(t, args)
		}
	}
}
