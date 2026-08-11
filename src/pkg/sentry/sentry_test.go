package sentry

import (
	"context"
	"testing"
)

func TestRecoverWithContextInvokesLocalPanicHookWithoutSentry(t *testing.T) {
	type contextKey string
	const key contextKey = "run"
	ctx := context.WithValue(context.Background(), key, "previous-run")

	var gotValue any
	var gotContextValue any
	SetPanicHook(func(hookCtx context.Context, recovered any) {
		gotValue = recovered
		gotContextValue = hookCtx.Value(key)
	})
	t.Cleanup(func() { SetPanicHook(nil) })

	func() {
		defer RecoverWithContext(ctx)
		panic("后台任务异常")
	}()

	if gotValue != "后台任务异常" {
		t.Fatalf("本地 panic hook 收到 %v", gotValue)
	}
	if gotContextValue != "previous-run" {
		t.Fatalf("panic hook 丢失 context，收到 %v", gotContextValue)
	}
}

func TestPanicHookFailureDoesNotReplaceOriginalRecovery(t *testing.T) {
	SetPanicHook(func(context.Context, any) {
		panic("诊断系统自身失败")
	})
	t.Cleanup(func() { SetPanicHook(nil) })

	// Recover 的既有语义是吞掉子 goroutine panic；hook 自身 panic 也不应泄漏。
	func() {
		defer Recover()
		panic("原始异常")
	}()
}
