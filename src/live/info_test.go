package live_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/types"
)

func TestInfoMarshalJSONIncludesPinnedState(t *testing.T) {
	ctrl := gomock.NewController(t)
	liveObj := livemock.NewMockLive(ctrl)
	liveObj.EXPECT().GetLiveId().Return(types.LiveID("test-room"))
	liveObj.EXPECT().GetRawUrl().Return("https://example.com/room")
	liveObj.EXPECT().GetPlatformCNName().Return("测试平台")
	liveObj.EXPECT().GetOptions().Return(&live.Options{})
	liveObj.EXPECT().GetLastStartTime().Return(time.Time{})

	data, err := json.Marshal(&live.Info{
		Live:   liveObj,
		Pinned: true,
	})
	assert.NoError(t, err)

	var result map[string]any
	assert.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, true, result["pinned"])
}
