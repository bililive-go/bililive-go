package utils

import (
	"io"

	"github.com/bililive-go/bililive-go/src/configs"
)

// DebugControlledWriter 在每次写入时读取当前全局配置的 Debug 值：
// - Debug=true: 将内容写入到目标 writer（通常是 os.Stdout/os.Stderr）
// - Debug=false: 丢弃输出（对被写入方表现为写入成功，以避免阻塞）
// 使用场景：子进程 stdout/stderr 的动态门控，以及其他高频日志/输出的运行时开关。
// 注意：该 writer 无缓冲也不做换行处理，仅按原样透传。
// 线程安全：无内部状态，依赖 configs.IsDebug() 的原子读，线程安全。

type debugControlledWriter struct {
	target io.Writer
}

func (w debugControlledWriter) Write(p []byte) (int, error) {
	if configs.IsDebug() {
		return w.target.Write(p)
	}
	return len(p), nil
}

// NewDebugControlledWriter 返回一个实现了 io.Writer 的包装器，
// 会根据全局 Debug 开关决定是否将写入透传到目标 writer。
func NewDebugControlledWriter(target io.Writer) io.Writer {
	return debugControlledWriter{target: target}
}
