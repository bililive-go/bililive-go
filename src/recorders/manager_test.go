package recorders

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	gomock "go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/types"
)

func TestManagerAddAndRemoveRecorder(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{
		Config: new(configs.Config),
	})
	m := NewManager(ctx)
	backup := newRecorder
	newRecorder = func(ctx context.Context, live live.Live) (Recorder, error) {
		r := NewMockRecorder(ctrl)
		r.EXPECT().Start(ctx).Return(nil)
		r.EXPECT().Close()
		return r, nil
	}
	defer func() { newRecorder = backup }()
	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()
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

func TestRecorderStartTimeStability(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	
	cfg := configs.NewConfig()
	cfg.Debug = true
	
	// The existing test doesn't directly create a recorder using NewRecorder, 
	// it mocks it. For our test, let's create a simple stability test
	// that doesn't require the full instance setup
	
	// Test that a recorder's StartTime method returns a stable time
	now := time.Now()
	r := &recorder{
		startTime: now,
	}
	
	startTime1 := r.StartTime()
	
	// Wait a small amount of time
	time.Sleep(10 * time.Millisecond)
	
	// Check that StartTime hasn't changed 
	startTime2 := r.StartTime()
	
	assert.Equal(t, startTime1, startTime2, "StartTime should remain stable")
	assert.Equal(t, now, startTime1, "StartTime should return the initial time")
}
