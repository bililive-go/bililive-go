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

// TestRestartRecorderRaceWithLiveEnd 验证 RestartRecorder 和 LiveEnd（RemoveRecorder）
// 并发执行时不会产生僵尸录制器。
//
// 问题场景：cronRestart 调用 RestartRecorder 的同时，listener 检测到直播结束触发 LiveEnd。
// 旧实现中 RestartRecorder 分别调用 RemoveRecorder 和 AddRecorder（各自独立获取锁），
// 导致 LiveEnd 的 HasRecorder 可能在两次操作的间隙返回 false，从而错过移除新录制器，
// 产生僵尸录制器不断发送请求。
//
// 修复后 RestartRecorder 在整个 map 替换操作期间持有锁，LiveEnd 无法看到中间状态。
//
// 测试策略：利用已有的 newRecorder 函数变量注入同步逻辑。RestartRecorder 在锁内
// 先从 map 中移除旧 recorder，然后调用 addRecorderLocked，后者调用 newRecorder
// 创建新录制器——此时仍持有写锁且新录制器尚未放入 map。
// 测试在 newRecorder 中通知 LiveEnd goroutine，由于写锁未释放，LiveEnd 的
// HasRecorder（需要读锁）会阻塞直到 restart 完成，确定性地验证中间状态不可见。
func TestRestartRecorderRaceWithLiveEnd(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	configs.SetCurrentConfig(new(configs.Config))
	ctx := context.WithValue(context.Background(), instance.Key, &instance.Instance{})
	mgr := NewManager(ctx)

	// restartPhase 标记当前 newRecorder 是初始 AddRecorder 调用还是 RestartRecorder 中的调用
	restartPhase := false
	// afterRemoveCh：在 restart 的 add 阶段（remove 已完成）通知 LiveEnd goroutine
	afterRemoveCh := make(chan struct{}, 1)

	backup := newRecorder
	newRecorder = func(ctx context.Context, l live.Live) (Recorder, error) {
		r := NewMockRecorder(ctrl)
		r.EXPECT().Start(gomock.Any()).Return(nil).AnyTimes()
		r.EXPECT().Close().AnyTimes()
		r.EXPECT().CloseForRestart().Return(nil).AnyTimes()

		if restartPhase {
			// RestartRecorder 的 add 阶段：此时旧 recorder 已从 map 移除，
			// 但 addRecorderLocked 还没有把新录制器放入 map。写锁仍被持有。
			// 通知 LiveEnd goroutine 可以尝试检查了。
			afterRemoveCh <- struct{}{}
		}

		return r, nil
	}
	defer func() { newRecorder = backup }()

	l := livemock.NewMockLive(ctrl)
	l.EXPECT().GetLiveId().Return(types.LiveID("test")).AnyTimes()
	l.EXPECT().GetLogger().Return(livelogger.New(0, nil)).AnyTimes()

	// 先正常添加一个录制器
	assert.NoError(t, mgr.AddRecorder(ctx, l))

	// 标记后续 newRecorder 调用为 restart 阶段
	restartPhase = true

	var wg sync.WaitGroup
	wg.Add(2)

	// 模拟 LiveEnd 事件处理器：等待 restart 的 remove 完成后执行检查
	var hasRecorderResult bool
	go func() {
		defer wg.Done()
		// 等待 RestartRecorder 完成 remove、进入 add 阶段
		<-afterRemoveCh
		// 此时 RestartRecorder 仍持有写锁（正在 addRecorderLocked 内部）
		// 修复后：HasRecorder 需要读锁，会阻塞直到 RestartRecorder 释放写锁
		// 释放时 add 已完成，HasRecorder 看到的是 restart 后的新录制器，返回 true
		// 旧实现：remove 和 add 分别获取锁，此时 HasRecorder 看到中间状态，返回 false
		hasRecorderResult = mgr.HasRecorder(ctx, "test")
		if hasRecorderResult {
			mgr.RemoveRecorder(ctx, "test")
		}
	}()

	// 模拟 cronRestart 触发的 RestartRecorder
	go func() {
		defer wg.Done()
		mgr.RestartRecorder(ctx, l)
	}()

	wg.Wait()

	// 验证：由于修复后 RestartRecorder 持有锁贯穿 remove+add，
	// LiveEnd 的 HasRecorder 在获得锁时看到的是 restart 后的新录制器（返回 true），
	// 然后 RemoveRecorder 正常移除。因此最终不应残留僵尸录制器。
	assert.False(t, mgr.HasRecorder(ctx, "test"),
		"发现僵尸录制器 - RestartRecorder 竞态条件未修复")

	// 验证 HasRecorder 在锁释放后应返回 true（说明它等到了 restart 完成，而不是看到中间状态 false）
	assert.True(t, hasRecorderResult,
		"HasRecorder 应在 RestartRecorder 完成后返回 true，说明锁正确阻止了中间状态暴露")
}
