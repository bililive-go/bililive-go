package servers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/types"
)

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

func TestUpdateRoomConfigByIDUpdatesPinnedState(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.File = ""
	cfg.LiveRooms = []configs.LiveRoom{{
		Url:    "https://example.com/room",
		LiveId: types.LiveID("test-room"),
	}}
	cfg.RefreshLiveRoomIndexCache()
	configs.SetCurrentConfig(cfg)

	request := httptest.NewRequest(
		"PATCH",
		"/api/config/rooms/id/test-room",
		bytes.NewBufferString(`{"pinned":true}`),
	)
	inst := &instance.Instance{Ctx: context.Background()}
	request = request.WithContext(context.WithValue(request.Context(), instance.Key, inst))
	request = mux.SetURLVars(request, map[string]string{"id": "test-room"})
	recorder := httptest.NewRecorder()

	updateRoomConfigById(recorder, request)

	assert.Equal(t, 200, recorder.Code)
	room, err := configs.GetCurrentConfig().GetLiveRoomByUrl("https://example.com/room")
	assert.NoError(t, err)
	assert.True(t, room.Pinned)
}
