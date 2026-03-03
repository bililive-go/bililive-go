package recorders

import (
	"context"
	"sync"
	"testing"

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

	configs.SetCurrentConfig(new(configs.Config))
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{})
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

// TestRestartRecorderRaceWithLiveEnd 验证 RestartRecorder 和 LiveEnd（RemoveRecorder）
// 并发执行时不会产生僵尸录制器。
//
// 问题场景：cronRestart 调用 RestartRecorder 的同时，listener 检测到直播结束触发 LiveEnd。
// 旧实现中 RestartRecorder 分别调用 RemoveRecorder 和 AddRecorder（各自独立获取锁），
// 导致 LiveEnd 的 HasRecorder 可能在两次操作的间隙返回 false，从而错过移除新录制器，
// 产生僵尸录制器不断发送请求。
//
// 修复后 RestartRecorder 在整个操作期间持有锁，LiveEnd 无法看到中间状态。
func TestRestartRecorderRaceWithLiveEnd(t *testing.T) {
	for iter := 0; iter < 100; iter++ {
		func() {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			configs.SetCurrentConfig(new(configs.Config))
			ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{})
			m := NewManager(ctx)

			backup := newRecorder
			newRecorder = func(ctx context.Context, l live.Live) (Recorder, error) {
				r := NewMockRecorder(ctrl)
				r.EXPECT().Start(gomock.Any()).Return(nil).AnyTimes()
				r.EXPECT().Close().AnyTimes()
				return r, nil
			}
			defer func() { newRecorder = backup }()

			l := livemock.NewMockLive(ctrl)
			l.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()

			assert.NoError(t, m.AddRecorder(ctx, l))

			var wg sync.WaitGroup
			wg.Add(2)

			// 模拟 cronRestart 触发的 RestartRecorder
			go func() {
				defer wg.Done()
				m.RestartRecorder(ctx, l)
			}()

			// 模拟 LiveEnd 事件处理器：先检查再移除
			go func() {
				defer wg.Done()
				if m.HasRecorder(ctx, "test") {
					m.RemoveRecorder(ctx, "test")
				}
			}()

			wg.Wait()

			// 两个操作都完成后，不应残留僵尸录制器
			if m.HasRecorder(ctx, "test") {
				t.Fatalf("iteration %d: 发现僵尸录制器 - RestartRecorder 竞态条件", iter)
			}
		}()
	}
}
