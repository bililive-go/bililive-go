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
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluele/gcache"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	_ "github.com/bililive-go/bililive-go/src/live/bilibili"
	"github.com/bililive-go/bililive-go/src/live/system"
	"github.com/bililive-go/bililive-go/src/recorders"
	"github.com/bililive-go/bililive-go/src/types"
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
	active := wrapped
	if !initializing {
		active = live.NewWrappedLive(inst.Ctx, original, inst.Cache)
		t.Cleanup(active.Close)
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

func TestApplyRoomLiveOptionsUsesLatestConfig(t *testing.T) {
	previous := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })

	const roomURL = "https://live.bilibili.com/123"
	liveID := types.LiveID(roomURL)
	stale := configs.LiveRoom{Url: roomURL, LiveId: liveID, Quality: 80, AudioOnly: true, NickName: "旧提交"}
	latest := configs.LiveRoom{Url: roomURL, LiveId: liveID, Quality: 10000, AudioOnly: false, NickName: "新提交"}
	cfg := configs.NewConfig()
	cfg.LiveRooms = []configs.LiveRoom{latest}
	cfg.RefreshLiveRoomIndexCache()
	configs.SetCurrentConfig(cfg)

	active := &recordingLive{rawUrl: roomURL}
	inst := &instance.Instance{Ctx: context.Background()}
	inst.Lives.Set(liveID, active)
	require.NoError(t, applyRoomLiveOptions(ctxWithInstance(inst), &stale, roomURL))
	require.Len(t, active.rooms, 1)
	assert.Equal(t, latest, active.rooms[0], "旧请求必须重新读取并应用最新配置")
}

func TestAddLiveImplReappliesConfigChangedDuringBuild(t *testing.T) {
	previousConfig := configs.GetCurrentConfig()
	previousBuilder := buildLiveForAdd
	t.Cleanup(func() {
		configs.SetCurrentConfig(previousConfig)
		buildLiveForAdd = previousBuilder
	})

	const roomURL = "https://live.bilibili.com/123"
	initial := configs.LiveRoom{Url: roomURL, Quality: 10000, NickName: "初始化快照"}
	cfg := configs.NewConfig()
	cfg.File = filepath.Join(t.TempDir(), "config.yml")
	cfg.LiveRooms = []configs.LiveRoom{initial}
	cfg.RefreshLiveRoomIndexCache()
	configs.SetCurrentConfig(cfg)
	require.NoError(t, cfg.Marshal())

	created := &recordingLive{rawUrl: roomURL}
	buildStarted := make(chan configs.LiveRoom, 1)
	releaseBuild := make(chan struct{})
	buildLiveForAdd = func(_ context.Context, room *configs.LiveRoom, _ gcache.Cache) (live.Live, error) {
		buildStarted <- *room
		<-releaseBuild
		return created, nil
	}

	inst := &instance.Instance{Cache: gcache.New(10).Build()}
	ctx := ctxWithInstance(inst)
	inst.Ctx = ctx
	listeners.NewManager(ctx)
	recorders.NewManager(ctx)

	addDone := make(chan error, 1)
	go func() {
		_, addErr := addLiveImpl(ctx, roomURL, false, false, true)
		addDone <- addErr
	}()
	builtFrom := <-buildStarted
	assert.Equal(t, initial.Quality, builtFrom.Quality)
	assert.Equal(t, initial.NickName, builtFrom.NickName)

	// Live 仍在耗时初始化、尚未注册时提交 PATCH；请求此时找不到运行态对象，
	// 但成功落盘的最新配置必须由注册路径在启动监听前补应用。
	rec := patchRoomConfig(t, inst, created, false, `{"audio_only":true,"quality":80,"nick_name":"PATCH 后配置"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	close(releaseBuild)

	select {
	case addErr := <-addDone:
		require.NoError(t, addErr)
	case <-time.After(time.Second):
		t.Fatal("Live 初始化释放后添加流程没有结束")
	}

	require.Len(t, created.rooms, 1)
	applied := created.rooms[0]
	assert.True(t, applied.AudioOnly)
	assert.Equal(t, 80, applied.Quality)
	assert.Equal(t, "PATCH 后配置", applied.NickName)
	registered, ok := inst.Lives.Get(created.GetLiveId())
	require.True(t, ok)
	assert.Same(t, created, registered)
}

func TestAddLiveImplOptionFailureDoesNotPublishRoom(t *testing.T) {
	previousConfig := configs.GetCurrentConfig()
	previousBuilder := buildLiveForAdd
	t.Cleanup(func() {
		configs.SetCurrentConfig(previousConfig)
		buildLiveForAdd = previousBuilder
	})

	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "新房间", true: "已有配置"}[existing], func(t *testing.T) {
			const roomURL = "https://live.bilibili.com/123"
			cfg := configs.NewConfig()
			cfg.File = filepath.Join(t.TempDir(), "config.yml")
			if existing {
				cfg.LiveRooms = []configs.LiveRoom{{Url: roomURL}}
			}
			cfg.RefreshLiveRoomIndexCache()
			configs.SetCurrentConfig(cfg)
			require.NoError(t, cfg.Marshal())

			created := &recordingLive{rawUrl: roomURL}
			buildLiveForAdd = func(context.Context, *configs.LiveRoom, gcache.Cache) (live.Live, error) {
				return &failingRoomConfigLive{Live: created}, nil
			}
			inst := &instance.Instance{Cache: gcache.New(10).Build()}
			ctx := ctxWithInstance(inst)
			inst.Ctx = ctx
			listenerManager := listeners.NewManager(ctx)

			_, err := addLiveImpl(ctx, roomURL, true, false, true)
			require.ErrorContains(t, err, "模拟运行态更新失败")
			assert.False(t, inst.Lives.Has(created.GetLiveId()))
			assert.False(t, listenerManager.HasListener(ctx, created.GetLiveId()))
			room, roomErr := configs.GetCurrentConfig().GetLiveRoomByUrl(roomURL)
			if existing {
				require.NoError(t, roomErr)
				assert.Empty(t, room.LiveId)
			} else {
				assert.Error(t, roomErr)
			}
		})
	}
}

type addTestListenerManager struct {
	listeners.Manager
	added bool
}

func (manager *addTestListenerManager) AddListener(context.Context, live.Live) error {
	manager.added = true
	return nil
}

func (manager *addTestListenerManager) HasListener(context.Context, types.LiveID) bool {
	return manager.added
}

func TestAddLiveImplPreservesRequestedListeningUnlessConfigChanged(t *testing.T) {
	previousConfig := configs.GetCurrentConfig()
	previousBuilder := buildLiveForAdd
	t.Cleanup(func() {
		configs.SetCurrentConfig(previousConfig)
		buildLiveForAdd = previousBuilder
	})

	for _, testCase := range []struct {
		name             string
		initial          bool
		requested        bool
		changeDuringInit bool
		latest           bool
		expected         bool
	}{
		{name: "请求启动监听", initial: false, requested: true, expected: true},
		{name: "请求不监听", initial: true, requested: false, expected: false},
		{name: "初始化期间关闭监听", initial: true, requested: true, changeDuringInit: true, latest: false, expected: false},
		{name: "初始化期间启动监听", initial: false, requested: false, changeDuringInit: true, latest: true, expected: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			const roomURL = "https://live.bilibili.com/123"
			cfg := configs.NewConfig()
			cfg.File = filepath.Join(t.TempDir(), "config.yml")
			cfg.LiveRooms = []configs.LiveRoom{{Url: roomURL, IsListening: testCase.initial}}
			cfg.RefreshLiveRoomIndexCache()
			configs.SetCurrentConfig(cfg)
			require.NoError(t, cfg.Marshal())

			created := &recordingLive{rawUrl: roomURL}
			buildLiveForAdd = func(_ context.Context, _ *configs.LiveRoom, _ gcache.Cache) (live.Live, error) {
				if testCase.changeDuringInit {
					_, err := configs.UpdateWithRetryTransient(func(current *configs.Config) error {
						current.LiveRooms[0].IsListening = testCase.latest
						return nil
					}, 3, time.Millisecond)
					require.NoError(t, err)
				}
				return created, nil
			}
			inst := &instance.Instance{Cache: gcache.New(10).Build()}
			ctx := ctxWithInstance(inst)
			inst.Ctx = ctx
			manager := &addTestListenerManager{}
			inst.ListenerManager = manager
			recorders.NewManager(ctx)

			_, err := addLiveImpl(ctx, roomURL, testCase.requested, false, true)
			require.NoError(t, err)
			assert.Equal(t, testCase.expected, manager.added)
		})
	}
}

type addFailureTrackingLive struct {
	*recordingLive
	closed bool
}

func (tracked *addFailureTrackingLive) Close() { tracked.closed = true }

func TestAddLiveImplAppendFailureCleansUpNewLive(t *testing.T) {
	previousConfig := configs.GetCurrentConfig()
	previousBuilder := buildLiveForAdd
	t.Cleanup(func() {
		configs.SetCurrentConfig(previousConfig)
		buildLiveForAdd = previousBuilder
	})

	const roomURL = "https://live.bilibili.com/123"
	cfg := configs.NewConfig()
	cfg.File = t.TempDir()
	configs.SetCurrentConfig(cfg)
	created := &addFailureTrackingLive{recordingLive: &recordingLive{rawUrl: roomURL}}
	buildLiveForAdd = func(_ context.Context, _ *configs.LiveRoom, cache gcache.Cache) (live.Live, error) {
		require.NoError(t, cache.Set(created, &live.Info{Live: created}))
		return created, nil
	}
	inst := &instance.Instance{Cache: gcache.New(10).Build()}
	ctx := ctxWithInstance(inst)
	inst.Ctx = ctx

	_, err := addLiveImpl(ctx, roomURL, true, false, true)
	require.Error(t, err)
	assert.False(t, inst.Lives.Has(created.GetLiveId()))
	assert.True(t, created.closed)
	_, cacheErr := inst.Cache.Get(created)
	assert.ErrorIs(t, cacheErr, gcache.KeyNotFoundError)
}

type handoffRoomConfigLive struct {
	*recordingLive
	updateStarted chan struct{}
	releaseUpdate chan struct{}
	updateCalls   atomic.Int32
}

func (l *handoffRoomConfigLive) UpdateLiveOptionsbyConfig(ctx context.Context, room *configs.LiveRoom) error {
	if l.updateCalls.Add(1) == 1 {
		close(l.updateStarted)
		<-l.releaseUpdate
	}
	return l.recordingLive.UpdateLiveOptionsbyConfig(ctx, room)
}

func TestApplyRoomLiveOptionsFollowsInitializingHandoff(t *testing.T) {
	previous := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })

	const roomURL = "https://live.bilibili.com/123"
	liveID := types.LiveID(roomURL)
	room := configs.LiveRoom{Url: roomURL, LiveId: liveID, AudioOnly: true}
	cfg := configs.NewConfig()
	cfg.LiveRooms = []configs.LiveRoom{room}
	cfg.RefreshLiveRoomIndexCache()
	configs.SetCurrentConfig(cfg)

	cache := gcache.New(10).Build()
	underlying := &handoffRoomConfigLive{
		recordingLive: &recordingLive{rawUrl: roomURL},
		updateStarted: make(chan struct{}),
		releaseUpdate: make(chan struct{}),
	}
	oldWrapped := live.NewWrappedLive(context.Background(), underlying, cache)
	newWrapped := live.NewWrappedLive(context.Background(), underlying, cache)
	t.Cleanup(oldWrapped.Close)
	t.Cleanup(newWrapped.Close)
	require.NoError(t, cache.Set(oldWrapped, &live.Info{Live: oldWrapped, AudioOnly: false}))
	require.NoError(t, cache.Set(newWrapped, &live.Info{Live: newWrapped, AudioOnly: false}))

	inst := &instance.Instance{Ctx: context.Background(), Cache: cache}
	inst.Lives.Set(liveID, oldWrapped)
	done := make(chan error, 1)
	go func() {
		done <- applyRoomLiveOptions(ctxWithInstance(inst), &room, roomURL)
	}()
	<-underlying.updateStarted

	// 模拟 RoomInitializingFinished 在应用过程中把旧包装器替换成新包装器。
	inst.Lives.Set(liveID, newWrapped)
	close(underlying.releaseUpdate)
	require.NoError(t, <-done)
	assert.Equal(t, int32(2), underlying.updateCalls.Load(), "换键后必须继续更新当前活动包装器")

	cached, err := cache.Get(newWrapped)
	require.NoError(t, err)
	assert.True(t, cached.(*live.Info).AudioOnly, "新包装器不能保留交接前的音频标志")
}

func TestApplyRoomLiveOptionsDoesNotBlockOtherRooms(t *testing.T) {
	previous := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })

	roomA := configs.LiveRoom{
		Url:     "https://live.bilibili.com/123",
		LiveId:  types.LiveID("https://live.bilibili.com/123"),
		Quality: 80,
	}
	roomB := configs.LiveRoom{
		Url:     "https://www.douyu.com/456",
		LiveId:  types.LiveID("https://www.douyu.com/456"),
		Quality: 10000,
	}
	cfg := configs.NewConfig()
	cfg.LiveRooms = []configs.LiveRoom{roomA, roomB}
	cfg.RefreshLiveRoomIndexCache()
	configs.SetCurrentConfig(cfg)

	blocked := &handoffRoomConfigLive{
		recordingLive: &recordingLive{rawUrl: roomA.Url},
		updateStarted: make(chan struct{}),
		releaseUpdate: make(chan struct{}),
	}
	fast := &recordingLive{rawUrl: roomB.Url}
	inst := &instance.Instance{Ctx: context.Background()}
	inst.Lives.Set(roomA.LiveId, blocked)
	inst.Lives.Set(roomB.LiveId, fast)
	ctx := ctxWithInstance(inst)

	released := false
	defer func() {
		if !released {
			close(blocked.releaseUpdate)
		}
	}()
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- applyRoomLiveOptions(ctx, &roomA, roomA.Url)
	}()
	<-blocked.updateStarted

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- applyRoomLiveOptions(ctx, &roomB, roomB.Url)
	}()
	select {
	case err := <-secondDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("一个房间的慢更新阻塞了另一个房间")
	}

	close(blocked.releaseUpdate)
	released = true
	require.NoError(t, <-firstDone)
	require.Len(t, fast.rooms, 1)
}

func TestApplyRoomLiveOptionsSerializesIdentityTransition(t *testing.T) {
	previous := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })

	const roomURL = "https://live.bilibili.com/123"
	liveID := types.LiveID("custom-live-id")
	latest := configs.LiveRoom{Url: roomURL, LiveId: liveID, AudioOnly: true}
	beforeIDBackfill := latest
	beforeIDBackfill.LiveId = ""
	cfg := configs.NewConfig()
	cfg.LiveRooms = []configs.LiveRoom{latest}
	cfg.RefreshLiveRoomIndexCache()
	configs.SetCurrentConfig(cfg)

	blocked := &handoffRoomConfigLive{
		recordingLive: &recordingLive{rawUrl: roomURL},
		updateStarted: make(chan struct{}),
		releaseUpdate: make(chan struct{}),
	}
	inst := &instance.Instance{Ctx: context.Background()}
	inst.Lives.Set(liveID, blocked)
	ctx := ctxWithInstance(inst)

	released := false
	defer func() {
		if !released {
			close(blocked.releaseUpdate)
		}
	}()
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- applyRoomLiveOptions(ctx, &beforeIDBackfill, roomURL)
	}()
	<-blocked.updateStarted

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- applyRoomLiveOptions(ctx, &latest, roomURL)
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("同一房间的 ID 回填前后快照没有共用运行态应用锁: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	assert.Equal(t, int32(1), blocked.updateCalls.Load(), "第二次更新应等待同一房间的第一次更新")

	close(blocked.releaseUpdate)
	released = true
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	assert.Equal(t, int32(2), blocked.updateCalls.Load())
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
			inst := &instance.Instance{Ctx: context.Background(), Cache: gcache.New(10).Build()}
			underlying := &recordingLive{rawUrl: tc.roomURL}
			active := live.NewWrappedLive(inst.Ctx, underlying, inst.Cache)
			t.Cleanup(active.Close)
			cfg.LiveRooms = []configs.LiveRoom{{Url: tc.roomURL, LiveId: active.GetLiveId()}}
			cfg.RefreshLiveRoomIndexCache()
			inst.Lives.Set(active.GetLiveId(), active)
			previousInfo := &live.Info{Live: active, AudioOnly: tc.cachedAudio}
			require.NoError(t, inst.Cache.Set(active, previousInfo))
			rec := patchRoomConfig(t, inst, active, true, tc.body)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Len(t, underlying.rooms, 1)
			cached, err := inst.Cache.Get(active)
			require.NoError(t, err)
			assert.Equal(t, tc.wantAudio, cached.(*live.Info).AudioOnly)
			assert.Equal(t, tc.cachedAudio, previousInfo.AudioOnly)
		})
	}
}
