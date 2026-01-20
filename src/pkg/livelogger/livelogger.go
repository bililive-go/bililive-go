package livelogger

import (
	"bytes"
	"context"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	applog "github.com/bililive-go/bililive-go/src/log"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
)

const (
	// DefaultBufferSize 默认日志缓冲区大小（64KB）
	DefaultBufferSize = 64 * 1024
)

// liveLoggerKey 是用于在 context 中存储 LiveLogger 引用的 key
type liveLoggerKey struct{}

var hookOnce sync.Once

// LogCallback 日志回调函数类型
// roomID: 直播间 ID，logLine: 单行日志
type LogCallback func(roomID string, logLine string)

// 全局日志回调
var (
	logCallbackMu sync.RWMutex
	logCallback   LogCallback
)

// SetLogCallback 设置全局日志回调
// 当任何 LiveLogger 产生新日志时会调用此回调
func SetLogCallback(cb LogCallback) {
	logCallbackMu.Lock()
	defer logCallbackMu.Unlock()
	logCallback = cb
}

// getLogCallback 获取当前的日志回调
func getLogCallback() LogCallback {
	logCallbackMu.RLock()
	defer logCallbackMu.RUnlock()
	return logCallback
}

// LiveLogHook 是一个 logrus Hook，负责将日志写入对应 live 的缓冲区
type LiveLogHook struct{}

// Levels 返回 Hook 监听的日志级别
func (h *LiveLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

// Fire 在日志触发时调用
func (h *LiveLogHook) Fire(entry *logrus.Entry) error {
	if entry.Context == nil {
		return nil
	}

	// 从 context 中获取 LiveLogger 引用
	logger, ok := entry.Context.Value(liveLoggerKey{}).(*LiveLogger)
	if !ok || logger == nil {
		return nil
	}

	// 格式化日志并写入缓冲区
	formatted, err := entry.Logger.Formatter.Format(entry)
	if err != nil {
		return nil // 忽略格式化错误
	}

	logger.writeToBuffer(formatted)
	return nil
}

// ensureHookRegistered 确保 Hook 已注册到全局 logger
func ensureHookRegistered() {
	hookOnce.Do(func() {
		applog.GetLogger().AddHook(&LiveLogHook{})
	})
}

// ringBuffer 是一个固定大小的环形缓冲区
type ringBuffer struct {
	buf      []byte
	size     int
	writePos int
	full     bool
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{
		buf:  make([]byte, size),
		size: size,
	}
}

func (rb *ringBuffer) Write(p []byte) (n int, err error) {
	n = len(p)
	if n == 0 {
		return 0, nil
	}

	// 如果写入的数据比缓冲区还大，只保留最后 size 字节
	if n >= rb.size {
		copy(rb.buf, p[n-rb.size:])
		rb.writePos = 0
		rb.full = true
		return n, nil
	}

	// 计算需要写入的位置
	remaining := rb.size - rb.writePos
	if n <= remaining {
		// 可以直接写入
		copy(rb.buf[rb.writePos:], p)
		rb.writePos += n
		if rb.writePos == rb.size {
			rb.writePos = 0
			rb.full = true
		}
	} else {
		// 需要绕回
		copy(rb.buf[rb.writePos:], p[:remaining])
		copy(rb.buf, p[remaining:])
		rb.writePos = n - remaining
		rb.full = true
	}

	return n, nil
}

func (rb *ringBuffer) String() string {
	if !rb.full {
		return string(rb.buf[:rb.writePos])
	}
	// 缓冲区已满，需要从 writePos 开始读取
	var result bytes.Buffer
	result.Write(rb.buf[rb.writePos:])
	result.Write(rb.buf[:rb.writePos])
	return result.String()
}

func (rb *ringBuffer) Bytes() []byte {
	if !rb.full {
		return rb.buf[:rb.writePos]
	}
	result := make([]byte, rb.size)
	copy(result, rb.buf[rb.writePos:])
	copy(result[rb.size-rb.writePos:], rb.buf[:rb.writePos])
	return result
}

// LiveLogger 是每个直播间专属的日志记录器
// 它嵌入 logrus.Entry，自动继承所有日志方法
// 通过 context 机制，Hook 可以识别出日志属于哪个 LiveLogger
type LiveLogger struct {
	*logrus.Entry
	mu     sync.RWMutex
	buffer *ringBuffer
	roomID string // 直播间 ID，用于日志回调
}

// New 创建一个新的 LiveLogger
// bufferSize: 缓冲区大小（字节），0 或负数使用默认值
// fields: 每条日志都会附带的字段（如 host, room）
func New(bufferSize int, fields logrus.Fields) *LiveLogger {
	return NewWithRoomID(bufferSize, fields, "")
}

// NewWithRoomID 创建一个新的 LiveLogger，带有 roomID
// bufferSize: 缓冲区大小（字节），0 或负数使用默认值
// fields: 每条日志都会附带的字段（如 host, room）
// roomID: 直播间 ID，用于日志推送回调
func NewWithRoomID(bufferSize int, fields logrus.Fields, roomID string) *LiveLogger {
	if bufferSize <= 0 {
		bufferSize = DefaultBufferSize
	}

	logger := &LiveLogger{
		buffer: newRingBuffer(bufferSize),
		roomID: roomID,
	}

	// 创建带有 LiveLogger 引用的 context
	// 这样 Hook 就能通过 context 找到对应的 LiveLogger
	ctx := context.WithValue(context.Background(), liveLoggerKey{}, logger)

	// 创建 Entry：先设置 context，再设置 fields
	// WithField/WithError 等方法会保留 context
	logger.Entry = applog.GetLogger().WithContext(ctx).WithFields(fields)

	// 确保 Hook 已注册
	ensureHookRegistered()

	return logger
}

// writeToBuffer 将日志写入缓冲区
func (l *LiveLogger) writeToBuffer(data []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buffer.Write(data)

	// 调用日志回调（如果有）
	if cb := getLogCallback(); cb != nil && l.roomID != "" {
		// 移除末尾换行符
		logLine := strings.TrimSuffix(string(data), "\n")
		bilisentry.Go(func() { cb(l.roomID, logLine) })
	}
}

// GetRoomID 获取直播间 ID
func (l *LiveLogger) GetRoomID() string {
	return l.roomID
}

// SetRoomID 设置直播间 ID
func (l *LiveLogger) SetRoomID(roomID string) {
	l.roomID = roomID
}

// GetLogs 获取缓冲区中的所有日志文本
func (l *LiveLogger) GetLogs() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.buffer.String()
}

// GetLogsBytes 获取缓冲区中的所有日志（字节形式）
func (l *LiveLogger) GetLogsBytes() []byte {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.buffer.Bytes()
}
