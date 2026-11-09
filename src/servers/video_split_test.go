package servers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestVideoSplitOverridesSetUpdateAndClear(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.VideoSplitStrategies = configs.VideoSplitStrategies{OnRoomNameChanged: true, MaxDuration: time.Hour, MaxFileSize: 2 * configs.GB}
	room := configs.LiveRoom{Url: "https://live.bilibili.com/1"}
	require.NoError(t, applyOverridableConfigUpdates(&room.OverridableConfig, map[string]interface{}{
		"video_split_strategies": map[string]interface{}{"on_room_name_changed": false, "max_duration": float64(10 * time.Minute), "max_file_size": "1GB"},
	}))
	require.NotNil(t, room.VideoSplitStrategies)
	require.Equal(t, configs.VideoSplitStrategies{MaxDuration: 10 * time.Minute, MaxFileSize: configs.GB}, cfg.ResolveConfigForRoom(&room, "bilibili").VideoSplitStrategies)
	require.NoError(t, applyOverridableConfigUpdates(&room.OverridableConfig, map[string]interface{}{
		"video_split_strategies": map[string]interface{}{"max_file_size": float64(0), "max_duration": float64(0)},
	}))
	require.Equal(t, configs.VideoSplitStrategies{}, *room.VideoSplitStrategies)
	require.NoError(t, applyOverridableConfigUpdates(&room.OverridableConfig, map[string]interface{}{}))
	require.NotNil(t, room.VideoSplitStrategies)
	require.NoError(t, applyOverridableConfigUpdates(&room.OverridableConfig, map[string]interface{}{"video_split_strategies": nil}))
	require.Nil(t, room.VideoSplitStrategies)
	require.Equal(t, cfg.VideoSplitStrategies, cfg.ResolveConfigForRoom(&room, "bilibili").VideoSplitStrategies)
	platform := configs.PlatformConfig{}
	require.NoError(t, applyOverridableConfigUpdates(&platform.OverridableConfig, map[string]interface{}{"video_split_strategies": map[string]interface{}{"max_file_size": "500MB"}}))
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

func TestInvalidVideoSplitUpdatesReturnBadRequestWithoutSaving(t *testing.T) {
	invalidValues := []string{
		`{"max_file_size":"1GiB"}`,
		`{"max_file_size":"1.5"}`,
		`{"max_file_size":true}`,
		`{"max_duration":64001000000.00001}`,
		`{"max_duration":"10m"}`,
		`{"on_room_name_changed":"true"}`,
		`{"max_file_size":null}`,
		`{"max_duration":-1}`,
		`{"max_duration":200000000}`,
		`{"max_duration":59000000000}`,
		`{"max_size":"1GB"}`,
		`{"max_file_size":-1}`,
		`"1GB"`,
		`[]`,
	}
	for _, endpoint := range []string{"平台", "房间ID", "房间URL"} {
		for _, value := range invalidValues {
			t.Run(endpoint+"/"+value, func(t *testing.T) {
				cfg, inst, active, _ := newRoomConfigTestLive(t, false, false)
				before, err := os.ReadFile(cfg.File)
				require.NoError(t, err)
				body := `{"nick_name":"不应保存","name":"不应保存","video_split_strategies":` + value + `}`
				var rec *httptest.ResponseRecorder
				if endpoint == "平台" {
					req := httptest.NewRequest(http.MethodPatch, "/api/config/platforms/bilibili", strings.NewReader(body)).WithContext(ctxWithInstance(inst))
					req = mux.SetURLVars(req, map[string]string{"platform": "bilibili"})
					rec = httptest.NewRecorder()
					updatePlatformConfig(rec, req)
				} else {
					rec = patchRoomConfig(t, inst, active, endpoint == "房间ID", body)
				}
				require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "video_split_strategies")
				require.Same(t, cfg, configs.GetCurrentConfig(), "无效请求不得提交内存配置")
				after, err := os.ReadFile(cfg.File)
				require.NoError(t, err)
				require.Equal(t, before, after, "无效请求不得写盘")
			})
		}
	}
}
