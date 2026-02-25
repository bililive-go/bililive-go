package recorders

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	gomock "go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/types"
)

func TestManagerAddAndRemoveRecorder(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	configs.SetCurrentConfig(new(configs.Config))
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{})
	m := NewManager(ctx)
	backup := newRecorder
	callCount := 0
	newRecorder = func(ctx context.Context, live live.Live) (Recorder, error) {
		callCount++
		r := NewMockRecorder(ctrl)
		r.EXPECT().Start(ctx).Return(nil)
		if callCount == 1 {
			// 第一个 recorder 会被 RestartRecorder 调用 CloseForRestart
			r.EXPECT().CloseForRestart().Return(nil)
		} else {
			// 第二个 recorder 会被 RemoveRecorder 调用 Close
			r.EXPECT().Close()
		}
		return r, nil
	}
	defer func() { newRecorder = backup }()
	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()
	l.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()
	assert.NoError(t, m.AddRecorder(context.Background(), l))
	assert.Equal(t, ErrRecorderExist, m.AddRecorder(context.Background(), l))
	ln, err := m.GetRecorder(context.Background(), "test")
	assert.NoError(t, err)
	assert.NotNil(t, ln)
	assert.True(t, m.HasRecorder(context.Background(), "test"))
	assert.NoError(t, m.RestartRecorder(context.Background(), l))
	assert.NoError(t, m.RemoveRecorder(context.Background(), "test"))
	assert.Equal(t, ErrRecorderNotExist, m.RemoveRecorder(context.Background(), "test"))
	_, err = m.GetRecorder(context.Background(), "test")
	assert.Equal(t, ErrRecorderNotExist, err)
	assert.False(t, m.HasRecorder(context.Background(), "test"))
}
