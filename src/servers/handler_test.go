package servers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/recorders"
	"github.com/bililive-go/bililive-go/src/types"
)

type removalTrackingRecorderManager struct {
	active  map[types.LiveID]bool
	removed []types.LiveID
}

func (m *removalTrackingRecorderManager) Start(context.Context) error { return nil }
func (m *removalTrackingRecorderManager) Close(context.Context)       {}
func (m *removalTrackingRecorderManager) AddRecorder(context.Context, live.Live) error {
	return nil
}
func (m *removalTrackingRecorderManager) RemoveRecorder(_ context.Context, id types.LiveID) error {
	if !m.active[id] {
		return recorders.ErrRecorderNotExist
	}
	delete(m.active, id)
	m.removed = append(m.removed, id)
	return nil
}
func (m *removalTrackingRecorderManager) RestartRecorder(context.Context, live.Live) error {
	return nil
}
func (m *removalTrackingRecorderManager) GetRecorder(context.Context, types.LiveID) (recorders.Recorder, error) {
	return nil, recorders.ErrRecorderNotExist
}
func (m *removalTrackingRecorderManager) HasRecorder(_ context.Context, id types.LiveID) bool {
	return m.active[id]
}
func (m *removalTrackingRecorderManager) GetAllParserPIDs() []int { return nil }
func (m *removalTrackingRecorderManager) GetRecorderStatus(context.Context, types.LiveID) (map[string]interface{}, error) {
	return nil, recorders.ErrRecorderNotExist
}
func (m *removalTrackingRecorderManager) GetActiveRecordingsCount() int { return len(m.active) }
func (m *removalTrackingRecorderManager) WaitForNotifications()         {}

func TestRemoveLiveImplRemovesRecorderWithoutListener(t *testing.T) {
	previousConfig := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(previousConfig) })

	const roomURL = "https://example.com/live/test"
	const liveID types.LiveID = "test-live"
	cfg := configs.NewConfig()
	cfg.File = filepath.Join(t.TempDir(), "config.yml")
	cfg.LiveRooms = []configs.LiveRoom{{Url: roomURL}}
	cfg.RefreshLiveRoomIndexCache()
	configs.SetCurrentConfig(cfg)
	require.NoError(t, cfg.Marshal())

	ctrl := gomock.NewController(t)
	liveObj := livemock.NewMockLive(ctrl)
	liveObj.EXPECT().GetLiveId().Return(liveID).AnyTimes()
	liveObj.EXPECT().GetRawUrl().Return(roomURL).AnyTimes()

	inst := &instance.Instance{}
	ctx := ctxWithInstance(inst)
	inst.Ctx = ctx
	listeners.NewManager(ctx)
	recorderMgr := &removalTrackingRecorderManager{active: map[types.LiveID]bool{liveID: true}}
	inst.RecorderManager = recorderMgr
	inst.Lives.Set(liveID, liveObj)

	require.NoError(t, removeLiveImpl(ctx, liveObj))
	assert.Equal(t, []types.LiveID{liveID}, recorderMgr.removed)
	assert.False(t, recorderMgr.HasRecorder(ctx, liveID))
	assert.False(t, inst.Lives.Has(liveID))
	_, err := configs.GetCurrentConfig().GetLiveRoomByUrl(roomURL)
	assert.Error(t, err)
}

func TestGetSoopLiveAuthConfigDoesNotExposeSavedPassword(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.SoopLiveAuth.Username = "tester"
	cfg.SoopLiveAuth.Password = "secret"
	configs.SetCurrentConfig(cfg)

	recorder := httptest.NewRecorder()
	getSoopLiveAuthConfig(recorder, nil)

	assert.Equal(t, 200, recorder.Code)

	var resp commonResp
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	assert.NoError(t, err)

	data, ok := resp.Data.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "tester", data["username"])
	assert.Equal(t, true, data["has_saved_credentials"])
	_, exists := data["password"]
	assert.False(t, exists)
}

func TestGetPlatformStatsReportsDisabledRateLimit(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.Interval = 1
	cfg.PlatformConfigs[configs.PlatformKeyDouyin] = configs.PlatformConfig{
		Name:                 "抖音",
		MinAccessIntervalSec: 60,
	}
	cfg.LiveRooms = []configs.LiveRoom{{
		Url:         "https://live.douyin.com/123456",
		IsListening: true,
	}}
	cfg.RefreshLiveRoomIndexCache()
	configs.SetCurrentConfig(cfg)
	t.Cleanup(func() { configs.SetCurrentConfig(configs.NewConfig()) })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/config/platforms", nil)
	request = request.WithContext(context.WithValue(request.Context(), instance.Key, &instance.Instance{}))
	getPlatformStats(recorder, request)

	assert.Equal(t, 200, recorder.Code)
	var response struct {
		PlatformRateLimitEnabled bool `json:"platform_rate_limit_enabled"`
		Platforms                []struct {
			PlatformKey    string `json:"platform_key"`
			WarningMessage string `json:"warning_message"`
		} `json:"platforms"`
	}
	assert.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.PlatformRateLimitEnabled)
	if assert.Len(t, response.Platforms, 1) {
		assert.Equal(t, configs.PlatformKeyDouyin, response.Platforms[0].PlatformKey)
		assert.Empty(t, response.Platforms[0].WarningMessage)
	}
}
