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

func TestSeamlessFileSwitching(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{
		Config: &configs.Config{
			VideoSplitStrategies: configs.VideoSplitStrategies{
				MaxDuration: time.Millisecond * 100, // Very short duration for testing
			},
		},
	})
	
	m := NewManager(ctx)
	
	// Track if SwitchFile was called instead of restart
	switchFileCalled := false
	
	backup := newRecorder
	newRecorder = func(ctx context.Context, live live.Live) (Recorder, error) {
		r := NewMockRecorder(ctrl)
		r.EXPECT().Start(ctx).Return(nil)
		r.EXPECT().StartTime().Return(time.Now().Add(-time.Minute*3)).AnyTimes() // Simulate old recording
		r.EXPECT().SwitchFile(ctx).DoAndReturn(func(ctx context.Context) error {
			switchFileCalled = true
			return nil
		}).AnyTimes()
		r.EXPECT().Close().AnyTimes()
		return r, nil
	}
	defer func() { newRecorder = backup }()
	
	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()
	
	// Add recorder with short max duration - this will trigger the cron automatically
	assert.NoError(t, m.AddRecorder(ctx, l))
	
	// Wait for the cron to trigger switching
	time.Sleep(200 * time.Millisecond)
	
	// Verify that SwitchFile was called
	assert.True(t, switchFileCalled, "SwitchFile should have been called automatically")
}