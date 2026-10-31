package servers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bluele/gcache"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	_ "github.com/bililive-go/bililive-go/src/live/bilibili"
	"github.com/bililive-go/bililive-go/src/live/system"
)

// 构造真实的平台 Live 和初始化包装，但不连接上游或启动监听器。
func newRoomConfigTestLive(t *testing.T, initializing, missingID bool) (*configs.Config, *instance.Instance, live.Live, live.Live) {
	t.Helper()
	previous := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })
	cfg := configs.NewConfig()
	cfg.File = filepath.Join(t.TempDir(), "config.yml")
	room := configs.LiveRoom{Url: "https://live.bilibili.com/123", Quality: 10000, NickName: "旧昵称"}
	configs.SetCurrentConfig(cfg)
	inst := &instance.Instance{Ctx: context.Background(), Cache: gcache.New(10).Build()}
	wrapped, err := live.NewInitializing(inst.Ctx, &room, inst.Cache, nil)
	require.NoError(t, err)
	t.Cleanup(wrapped.Close)
	original := wrapped.(*live.WrappedLive).Live.(*system.InitializingLive).OriginalLive
	active := original
	if initializing {
		active = wrapped
	}
	if !missingID {
		room.LiveId = active.GetLiveId()
	}
	cfg.LiveRooms = []configs.LiveRoom{room}
	cfg.RefreshLiveRoomIndexCache()
	require.NoError(t, cfg.Marshal())
	inst.Lives.Set(active.GetLiveId(), active)
	return cfg, inst, active, original
}

func patchRoomConfig(t *testing.T, inst *instance.Instance, room live.Live, byID bool, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/config/rooms/", strings.NewReader(body)).WithContext(ctxWithInstance(inst))
	rec := httptest.NewRecorder()
	if byID {
		req = mux.SetURLVars(req, map[string]string{"id": string(room.GetLiveId())})
		updateRoomConfigById(rec, req)
	} else {
		req = mux.SetURLVars(req, map[string]string{"url": url.QueryEscape(room.GetRawUrl())})
		updateRoomConfig(rec, req)
	}
	return rec
}

func TestRoomConfigUpdatesLiveOptions(t *testing.T) {
	for _, byID := range []bool{true, false} {
		entry := "URL入口"
		if byID {
			entry = "ID入口"
		}
		for _, initializing := range []bool{false, true} {
			state := "普通Live"
			if initializing {
				state = "初始化Live"
			}
			for _, missingID := range []bool{false, true} {
				identity := "已有ID"
				if missingID {
					identity = "未回填ID"
				}
				t.Run(entry+"/"+state+"/"+identity, func(t *testing.T) {
					cfg, inst, active, original := newRoomConfigTestLive(t, initializing, missingID)
					unrelated := &recordingLive{rawUrl: "https://live.bilibili.com/456"}
					inst.Lives.Set(unrelated.GetLiveId(), unrelated)
					for _, change := range []struct {
						body      string
						audioOnly bool
						quality   int
						nickName  string
					}{
						{`{"audio_only":true,"quality":80,"nick_name":"新昵称"}`, true, 80, "新昵称"},
						{`{"audio_only":false,"quality":0,"nick_name":""}`, false, 0, ""},
					} {
						previousInfo := &live.Info{Live: active, AudioOnly: active.GetOptions().AudioOnly, RoomName: "直播间标题", Status: true}
						previousAudioOnly := previousInfo.AudioOnly
						require.NoError(t, inst.Cache.Set(active, previousInfo))
						rec := patchRoomConfig(t, inst, active, byID, change.body)
						require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
						for _, obj := range []live.Live{active, original} {
							assert.Equal(t, change.audioOnly, obj.GetOptions().AudioOnly)
							assert.Equal(t, change.quality, obj.GetOptions().Quality)
							assert.Equal(t, change.nickName, obj.GetOptions().NickName)
						}
						cached, err := inst.Cache.Get(active)
						require.NoError(t, err)
						currentInfo := cached.(*live.Info)
						assert.Equal(t, change.audioOnly, currentInfo.AudioOnly)
						assert.Equal(t, previousAudioOnly, previousInfo.AudioOnly, "不能修改当前录制持有的信息")
						assert.Equal(t, previousInfo.RoomName, currentInfo.RoomName)
						assert.True(t, currentInfo.Status)
						stored, err := os.ReadFile(cfg.File)
						require.NoError(t, err)
						var disk configs.Config
						require.NoError(t, yaml.Unmarshal(stored, &disk))
						for _, current := range []*configs.Config{configs.GetCurrentConfig(), &disk} {
							room, err := current.GetLiveRoomByUrl(active.GetRawUrl())
							require.NoError(t, err)
							assert.Equal(t, change.audioOnly, room.AudioOnly)
							assert.Equal(t, change.quality, room.Quality)
							assert.Equal(t, change.nickName, room.NickName)
						}
					}
					assert.Empty(t, unrelated.rooms, "不应刷新其他直播间")
					assert.Equal(t, 10000, cfg.LiveRooms[0].Quality, "不能修改旧配置快照")
				})
			}
		}
	}
}

func TestRoomConfigSaveFailureKeepsLiveOptions(t *testing.T) {
	for _, byID := range []bool{true, false} {
		entry := "URL入口"
		if byID {
			entry = "ID入口"
		}
		t.Run(entry, func(t *testing.T) {
			cfg, inst, active, original := newRoomConfigTestLive(t, true, false)
			previousInfo := &live.Info{Live: active, AudioOnly: false}
			require.NoError(t, inst.Cache.Set(active, previousInfo))
			cfg.File = filepath.Join(t.TempDir(), "不存在的目录", "config.yml")
			rec := patchRoomConfig(t, inst, active, byID, `{"audio_only":true,"quality":80,"nick_name":"新昵称"}`)
			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			assert.Same(t, cfg, configs.GetCurrentConfig())
			cached, err := inst.Cache.Get(active)
			require.NoError(t, err)
			assert.Same(t, previousInfo, cached)
			for _, obj := range []live.Live{active, original} {
				assert.False(t, obj.GetOptions().AudioOnly)
				assert.Equal(t, 10000, obj.GetOptions().Quality)
				assert.Equal(t, "旧昵称", obj.GetOptions().NickName)
			}
		})
	}
}

type failingRoomConfigLive struct{ live.Live }

func (l *failingRoomConfigLive) UpdateLiveOptionsbyConfig(context.Context, *configs.LiveRoom) error {
	return errors.New("模拟运行态更新失败")
}

func TestRoomConfigApplyFailureIsReported(t *testing.T) {
	for _, byID := range []bool{true, false} {
		entry := "URL入口"
		if byID {
			entry = "ID入口"
		}
		t.Run(entry, func(t *testing.T) {
			_, inst, active, _ := newRoomConfigTestLive(t, false, false)
			inst.Lives.Set(active.GetLiveId(), &failingRoomConfigLive{Live: active})
			rec := patchRoomConfig(t, inst, active, byID, `{"audio_only":true}`)
			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "已保存，但应用运行时选项失败")
			assert.True(t, configs.GetCurrentConfig().LiveRooms[0].AudioOnly)
			assert.False(t, active.GetOptions().AudioOnly)
		})
	}
}

func TestRoomConfigRespectsPlatformAudioMode(t *testing.T) {
	for _, tc := range []struct {
		name        string
		roomURL     string
		cachedAudio bool
		body        string
		wantAudio   bool
	}{
		{"猫耳保持纯音频", "https://fm.missevan.com/live/123", true, `{"nick_name":"新昵称"}`, true},
		{"其他平台保持视频", "https://www.douyu.com/123", false, `{"audio_only":true}`, false},
		{"Soop开启音频", "https://play.sooplive.com/test", false, `{"audio_only":true}`, true},
		{"Soop关闭音频", "https://play.sooplive.com/test", true, `{"audio_only":false}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := configs.GetCurrentConfig()
			t.Cleanup(func() { configs.SetCurrentConfig(previous) })
			cfg := writableConfig(t)
			active := &recordingLive{rawUrl: tc.roomURL}
			cfg.LiveRooms = []configs.LiveRoom{{Url: tc.roomURL, LiveId: active.GetLiveId()}}
			cfg.RefreshLiveRoomIndexCache()
			inst := &instance.Instance{Ctx: context.Background(), Cache: gcache.New(10).Build()}
			inst.Lives.Set(active.GetLiveId(), active)
			previousInfo := &live.Info{Live: active, AudioOnly: tc.cachedAudio}
			require.NoError(t, inst.Cache.Set(active, previousInfo))
			rec := patchRoomConfig(t, inst, active, true, tc.body)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Len(t, active.rooms, 1)
			cached, err := inst.Cache.Get(active)
			require.NoError(t, err)
			assert.Equal(t, tc.wantAudio, cached.(*live.Info).AudioOnly)
			assert.Equal(t, tc.cachedAudio, previousInfo.AudioOnly)
		})
	}
}
