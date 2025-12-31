package utils

import (
	"bytes"
	"io"
	"strings"
	"sync"

	"github.com/bililive-go/bililive-go/src/configs"
)

// LogFilterWriter 类似于 DebugControlledWriter，但在 Debug=false 时
// 也会透传包含特定关键字（如 "error", "fatal"）的行，以避免由于静音而丢失关键错误信息。
type logFilterWriter struct {
	target   io.Writer
	keywords []string
	buf      []byte
	mu       sync.Mutex
}

func (w *logFilterWriter) Write(p []byte) (int, error) {
	if configs.IsDebug() {
		return w.target.Write(p)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 简单的按行缓冲处理，避免把行截断导致关键字匹配失败
	// 注意：这只是一个简单的实现，可能无法处理非常长的行或不规范的输出
	w.buf = append(w.buf, p...)

	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}

		line := w.buf[:idx+1] // 包含换行符
		lineStr := strings.ToLower(string(line))

		shouldWrite := false
		for _, kw := range w.keywords {
			if strings.Contains(lineStr, kw) {
				shouldWrite = true
				break
			}
		}

		if shouldWrite {
			w.target.Write(line)
		}

		w.buf = w.buf[idx+1:]
	}

	return len(p), nil
}

func NewLogFilterWriter(target io.Writer, keywords ...string) io.Writer {
	if len(keywords) == 0 {
		keywords = []string{"error", "fatal", "fail", "exception"}
	}
	return &logFilterWriter{
		target:   target,
		keywords: keywords,
	}
}
