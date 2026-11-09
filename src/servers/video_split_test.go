package servers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestVideoSplitOverridesSetUpdateAndClear(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.VideoSplitStrategies = configs.VideoSplitStrategies{OnRoomNameChanged: true, MaxDuration: time.Hour, MaxFileSize: 2 * configs.GB}
	room := configs.LiveRoom{Url: "https://live.bilibili.com/1"}
	applyOverridableConfigUpdates(&room.OverridableConfig, map[string]interface{}{
		"video_split_strategies": map[string]interface{}{"on_room_name_changed": false, "max_duration": float64(10 * time.Minute), "max_file_size": "1GB"},
	})
	require.NotNil(t, room.VideoSplitStrategies)
	require.Equal(t, configs.VideoSplitStrategies{MaxDuration: 10 * time.Minute, MaxFileSize: configs.GB}, cfg.ResolveConfigForRoom(&room, "bilibili").VideoSplitStrategies)
	applyOverridableConfigUpdates(&room.OverridableConfig, map[string]interface{}{
		"video_split_strategies": map[string]interface{}{"max_file_size": float64(0), "max_duration": float64(0)},
	})
	require.Equal(t, configs.VideoSplitStrategies{}, *room.VideoSplitStrategies)
	applyOverridableConfigUpdates(&room.OverridableConfig, map[string]interface{}{})
	require.NotNil(t, room.VideoSplitStrategies)
	applyOverridableConfigUpdates(&room.OverridableConfig, map[string]interface{}{"video_split_strategies": nil})
	require.Nil(t, room.VideoSplitStrategies)
	require.Equal(t, cfg.VideoSplitStrategies, cfg.ResolveConfigForRoom(&room, "bilibili").VideoSplitStrategies)
	platform := configs.PlatformConfig{}
	applyOverridableConfigUpdates(&platform.OverridableConfig, map[string]interface{}{"video_split_strategies": map[string]interface{}{"max_file_size": "500MB"}})
	cfg.PlatformConfigs = map[string]configs.PlatformConfig{"bilibili": platform}
	require.Equal(t, 500*configs.MB, cfg.ResolveConfigForRoom(&room, "bilibili").VideoSplitStrategies.MaxFileSize)
}

func TestRoomVideoSplitPatchPersistsAndClears(t *testing.T) {
	for _, byID := range []bool{false, true} {
		t.Run(map[bool]string{false: "URL", true: "ID"}[byID], func(t *testing.T) {
			cfg, inst, active, _ := newRoomConfigTestLive(t, false, false)
			for _, body := range []string{
				`{"video_split_strategies":{"on_room_name_changed":true,"max_duration":600000000000,"max_file_size":"1GB"}}`,
				`{"video_split_strategies":null}`,
			} {
				rec := patchRoomConfig(t, inst, active, byID, body)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				data, err := os.ReadFile(cfg.File)
				require.NoError(t, err)
				var disk configs.Config
				require.NoError(t, yaml.Unmarshal(data, &disk))
				for _, current := range []*configs.Config{configs.GetCurrentConfig(), &disk} {
					room, err := current.GetLiveRoomByUrl(active.GetRawUrl())
					require.NoError(t, err)
					if body == `{"video_split_strategies":null}` {
						require.Nil(t, room.VideoSplitStrategies)
					} else {
						require.Equal(t, &configs.VideoSplitStrategies{OnRoomNameChanged: true, MaxDuration: 10 * time.Minute, MaxFileSize: configs.GB}, room.VideoSplitStrategies)
					}
				}
			}
		})
	}
}

func TestPlatformStatsIncludesVideoSplitOverrides(t *testing.T) {
	cfg := configs.NewConfig()
	platformSplit := &configs.VideoSplitStrategies{MaxDuration: time.Hour}
	roomSplit := &configs.VideoSplitStrategies{MaxFileSize: configs.GB}
	cfg.PlatformConfigs = map[string]configs.PlatformConfig{
		"bilibili": {OverridableConfig: configs.OverridableConfig{VideoSplitStrategies: platformSplit}},
	}
	cfg.LiveRooms = []configs.LiveRoom{{Url: "https://live.bilibili.com/1", OverridableConfig: configs.OverridableConfig{VideoSplitStrategies: roomSplit}}}
	previous := configs.GetCurrentConfig()
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })
	req := httptest.NewRequest(http.MethodGet, "/api/config/platforms", nil)
	req = req.WithContext(context.WithValue(req.Context(), instance.Key, &instance.Instance{}))
	rec := httptest.NewRecorder()
	getPlatformStats(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var response struct {
		Platforms []struct {
			VideoSplitStrategies *configs.VideoSplitStrategies `json:"video_split_strategies"`
			Rooms                []struct {
				VideoSplitStrategies *configs.VideoSplitStrategies `json:"video_split_strategies"`
			} `json:"rooms"`
		} `json:"platforms"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response.Platforms, 1)
	require.Equal(t, platformSplit, response.Platforms[0].VideoSplitStrategies)
	require.Len(t, response.Platforms[0].Rooms, 1)
	require.Equal(t, roomSplit, response.Platforms[0].Rooms[0].VideoSplitStrategies)
}
