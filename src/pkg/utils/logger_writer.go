package utils

import (
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
)

// LineHandler 定义行处理函数类型
// line: 要处理的行（不含换行符）
// isImportant: 该行是否包含重要关键字（error, fatal 等）
type LineHandler func(line string, isImportant bool)

// FilteredLineWriter 是一个按行缓冲的 io.Writer
// 支持关键字过滤，在非 Debug 模式下只处理包含关键字的行
//
// 使用固定大小的字节缓冲区避免频繁内存分配：
// - 使用预分配的固定缓冲区，避免 append 导致的重新分配
// - 当缓冲区满时强制输出，然后重置写入位置
// - 不使用切片操作来截取已处理的数据，而是移动未处理的数据到缓冲区头部
// - 强制输出时会检查 UTF-8 边界，避免截断多字节字符
type FilteredLineWriter struct {
	handler  LineHandler
	keywords []string
	buf      []byte // 固定大小的缓冲区
	pos      int    // 当前写入位置
	mu       sync.Mutex
}

const (
	// DefaultBufSize 默认缓冲区大小
	DefaultBufSize = 8192
	// MaxLineLength 单行最大长度，超过此长度强制输出
	MaxLineLength = 4096
)

// DefaultKeywords 默认的过滤关键字
var DefaultKeywords = []string{"error", "fatal", "fail", "exception", "warning", "warn"}

// NewFilteredLineWriter 创建一个新的 FilteredLineWriter
func NewFilteredLineWriter(handler LineHandler, keywords ...string) *FilteredLineWriter {
	if len(keywords) == 0 {
		keywords = DefaultKeywords
	}
	return &FilteredLineWriter{
		handler:  handler,
		keywords: keywords,
		buf:      make([]byte, DefaultBufSize),
		pos:      0,
	}
}

func (w *FilteredLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	written := len(p)
	for len(p) > 0 {
		// 计算可写入的空间
		space := len(w.buf) - w.pos
		if space == 0 {
			// 缓冲区满了，强制处理
			w.flushBuffer()
			space = len(w.buf)
		}

		// 写入尽可能多的数据
		n := len(p)
		if n > space {
			n = space
		}
		copy(w.buf[w.pos:], p[:n])
		w.pos += n
		p = p[n:]

		// 处理完整的行
		w.processLines()
	}

	return written, nil
}

// processLines 处理缓冲区中的完整行
func (w *FilteredLineWriter) processLines() {
	start := 0
	for i := 0; i < w.pos; i++ {
		if w.buf[i] == '\n' {
			// 找到一个完整的行
			line := string(w.buf[start:i])
			start = i + 1

			if strings.TrimSpace(line) != "" {
				w.handleLine(line)
			}
		}
	}

	// 移动未处理的数据到缓冲区头部
	if start > 0 {
		remaining := w.pos - start
		if remaining > 0 {
			copy(w.buf, w.buf[start:w.pos])
		}
		w.pos = remaining
	}

	// 如果剩余未处理的数据过长（没有换行符的连续数据），强制输出
	// 这个检查放在移动数据之后，确保检查的是当前未处理的数据长度
	if w.pos > MaxLineLength {
		// 找到安全的 UTF-8 边界
		safeEnd := findUTF8SafeBoundary(w.buf[:w.pos])
		line := string(w.buf[:safeEnd])
		// 移动剩余数据到缓冲区头部
		remaining := w.pos - safeEnd
		if remaining > 0 {
			copy(w.buf, w.buf[safeEnd:w.pos])
		}
		w.pos = remaining
		w.handleLine(line)
	}
}

// flushBuffer 强制输出缓冲区中的所有内容
func (w *FilteredLineWriter) flushBuffer() {
	if w.pos > 0 {
		// 找到安全的 UTF-8 边界
		safeEnd := findUTF8SafeBoundary(w.buf[:w.pos])
		if safeEnd > 0 {
			line := string(w.buf[:safeEnd])
			// 移动剩余的不完整字符到缓冲区头部
			remaining := w.pos - safeEnd
			if remaining > 0 {
				copy(w.buf, w.buf[safeEnd:w.pos])
			}
			w.pos = remaining
			if strings.TrimSpace(line) != "" {
				w.handleLine(line)
			}
		}
		// 如果 safeEnd == 0，说明缓冲区中只有不完整的 UTF-8 字符，保留等待更多数据
	}
}

// findUTF8SafeBoundary 找到安全的 UTF-8 边界位置
// 返回不会截断多字节字符的最大位置
func findUTF8SafeBoundary(data []byte) int {
	n := len(data)
	if n == 0 {
		return 0
	}

	// 从末尾向前查找，检查是否有不完整的 UTF-8 字符
	// UTF-8 编码规则：
	// - 0xxxxxxx: 单字节字符 (ASCII)
	// - 110xxxxx 10xxxxxx: 双字节字符
	// - 1110xxxx 10xxxxxx 10xxxxxx: 三字节字符
	// - 11110xxx 10xxxxxx 10xxxxxx 10xxxxxx: 四字节字符
	// - 10xxxxxx: 续字节

	// 从末尾检查最多 4 个字节（UTF-8 最大字符长度）
	for i := n - 1; i >= 0 && i >= n-4; i-- {
		b := data[i]
		if b&0x80 == 0 {
			// ASCII 字符，可以安全截断到这里之后
			return n
		}
		if b&0xC0 == 0xC0 {
			// 找到了多字节字符的起始字节
			// 计算这个字符需要多少字节
			var charLen int
			if b&0xF8 == 0xF0 {
				charLen = 4
			} else if b&0xF0 == 0xE0 {
				charLen = 3
			} else if b&0xE0 == 0xC0 {
				charLen = 2
			} else {
				// 无效的起始字节，跳过
				continue
			}
			// 检查这个字符是否完整
			if i+charLen <= n {
				// 字符完整，可以安全截断到末尾
				return n
			}
			// 字符不完整，截断到这个字符之前
			return i
		}
		// 10xxxxxx 续字节，继续向前查找
	}

	// 检查整个数据是否是有效的 UTF-8
	if utf8.Valid(data) {
		return n
	}

	return n
}

func (w *FilteredLineWriter) handleLine(line string) {
	if w.handler == nil {
		return
	}

	lineLower := strings.ToLower(line)
	isImportant := false
	for _, kw := range w.keywords {
		if strings.Contains(lineLower, kw) {
			isImportant = true
			break
		}
	}

	// Debug 模式下输出所有行，否则只输出重要的行
	if configs.IsDebug() || isImportant {
		w.handler(line, isImportant)
	}
}

// Flush 强制输出缓冲区中的所有内容（公开方法）
func (w *FilteredLineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushBuffer()
}

// ============ 便捷构造函数 ============

// NewLogFilterWriter 创建一个将过滤后的行写入 io.Writer 的 writer
// 在 Debug 模式下输出所有内容，否则只输出包含关键字的行
func NewLogFilterWriter(target io.Writer, keywords ...string) io.Writer {
	return NewFilteredLineWriter(func(line string, isImportant bool) {
		target.Write([]byte(line + "\n"))
	}, keywords...)
}

// NewLoggerWriter 创建一个将过滤后的行写入 LiveLogger 的 writer
// 会根据内容自动选择日志级别（Error/Warn/Debug）
func NewLoggerWriter(logger *livelogger.LiveLogger, keywords ...string) io.Writer {
	if logger == nil {
		return io.Discard
	}
	return NewFilteredLineWriter(func(line string, isImportant bool) {
		if !isImportant {
			logger.Debug(line)
			return
		}
		// 根据关键字类型选择日志级别
		lineLower := strings.ToLower(line)
		if strings.Contains(lineLower, "error") || strings.Contains(lineLower, "fatal") {
			logger.Error(line)
		} else if strings.Contains(lineLower, "warning") || strings.Contains(lineLower, "warn") {
			logger.Warn(line)
		} else {
			logger.Info(line)
		}
	}, keywords...)
}
