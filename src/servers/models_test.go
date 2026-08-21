package servers

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/types"
)

func TestLiveSliceSortsPinnedRoomsFirst(t *testing.T) {
	ctrl := gomock.NewController(t)
	newInfo := func(id string, pinned bool) *live.Info {
		liveObj := livemock.NewMockLive(ctrl)
		liveObj.EXPECT().GetLiveId().Return(types.LiveID(id)).AnyTimes()
		return &live.Info{
			Live:   liveObj,
			Pinned: pinned,
		}
	}

	lives := liveSlice{
		newInfo("normal-b", false),
		newInfo("pinned-b", true),
		newInfo("pinned-a", true),
		newInfo("normal-a", false),
	}

	sort.Sort(lives)

	ids := make([]types.LiveID, 0, len(lives))
	for _, info := range lives {
		ids = append(ids, info.Live.GetLiveId())
	}
	assert.Equal(t, []types.LiveID{"pinned-a", "pinned-b", "normal-a", "normal-b"}, ids)
}
