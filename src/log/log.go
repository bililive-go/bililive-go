package log

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/interfaces"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
)

var (
	stopDebugWatcher context.CancelFunc
	debugWatcherDone chan struct{}
	activeLogSyncers []interface{ Sync() error }
	watcherMu        sync.Mutex
)

func New(ctx context.Context) *interfaces.Logger {
	cfg := configs.GetCurrentConfig()
	logLevel := logrus.InfoLevel
	if cfg != nil && cfg.Debug {
		logLevel = logrus.DebugLevel
	}
	config := cfg
	writers := []io.Writer{os.Stderr}

	// 收集需要在关闭时释放的文件句柄
	var closers []io.Closer

	// 检测是否由 Launcher 启动（版本切换场景）
	isLauncherManaged := os.Getenv("BILILIVE_LAUNCHER") == "1"

	outputFolder := config.Log.OutPutFolder
	outputInfo, err := os.Stat(outputFolder)
	if err != nil {
		log.Fatalf("err: \"%s\", Failed to determine log output folder: %s", err, outputFolder)
	} else if !outputInfo.IsDir() {
		log.Fatalf("Failed to determine log output folder: %s is not a directory", outputFolder)
	} else {
		if config.Log.SaveEveryLog {
			logFile, logLocation, err := openUniqueRunLog(outputFolder, time.Now())
			if err != nil {
				log.Fatalf("Failed to open log file %s for output: %s", logLocation, err)
			} else {
				writers = append(writers, logFile)
				closers = append(closers, logFile)
			}
		}
		if config.Log.SaveLastLog {
			// 不在启动时删除旧日志。异常退出后的下一次启动必须仍能调查上一运行；
			// 过期日志只交给 dailyRotatingWriter 的 RotateDays 保留策略清理。
			// 按天滚动写入日志（使用 O_APPEND 追加模式，不会覆盖已有内容）
			rot := newDailyRotatingWriter(outputFolder, "bililive-go", config.Log.RotateDays)
			writers = append(writers, rot)
			closers = append(closers, rot)
		}
	}

	logrus.SetOutput(io.MultiWriter(writers...))
	logrus.SetFormatter(&logrus.TextFormatter{
		DisableColors:   true,
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
	if config.Debug {
		logrus.SetReportCaller(true)
	}

	// 全局唯一 logger 使用 logrus 标准 logger
	logrus.SetLevel(logLevel)

	// 动态监听 Debug 变化，实时调整日志级别与是否打印调用方
	watcherMu.Lock()
	if stopDebugWatcher != nil {
		stopDebugWatcher()
		if debugWatcherDone != nil {
			<-debugWatcherDone
		}
	}
	watcherCtx, cancel := context.WithCancel(ctx)
	watcherDone := make(chan struct{})
	stopDebugWatcher = cancel
	debugWatcherDone = watcherDone
	activeLogSyncers = activeLogSyncers[:0]
	for _, closer := range closers {
		if syncer, ok := closer.(interface{ Sync() error }); ok {
			activeLogSyncers = append(activeLogSyncers, syncer)
		}
	}
	watcherMu.Unlock()

	bilisentry.GoWithContext(watcherCtx, func(ctx context.Context) {
		defer close(watcherDone)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		prev := config.Debug
		for {
			select {
			case <-ctx.Done():
				// context 取消时关闭所有日志文件句柄
				for _, c := range closers {
					if syncer, ok := c.(interface{ Sync() error }); ok {
						_ = syncer.Sync()
					}
					_ = c.Close()
				}
				return
			case <-ticker.C:
				now := configs.IsDebug()
				if now == prev {
					continue
				}
				if now {
					logrus.SetLevel(logrus.DebugLevel)
					logrus.SetReportCaller(true)
				} else {
					logrus.SetLevel(logrus.InfoLevel)
					logrus.SetReportCaller(false)
				}
				prev = now
			}
		}
	})

	// 版本切换场景：写入分隔标记
	if isLauncherManaged {
		logrus.Infof("====== 由 Launcher 启动（版本切换） ======")
	}

	return &interfaces.Logger{Logger: logrus.StandardLogger()}
}

// Close 同步并关闭当前日志文件，且等待后台 watcher 退出。
// 正常 shutdown 必须显式调用它，不能只依赖已取消的应用 root context。
func Close() {
	watcherMu.Lock()
	cancel := stopDebugWatcher
	done := debugWatcherDone
	stopDebugWatcher = nil
	debugWatcherDone = nil
	activeLogSyncers = nil
	if cancel != nil {
		cancel()
	}
	watcherMu.Unlock()
	if done != nil {
		<-done
	}
}

// Sync 把当前文本日志的确定字节前缀刷入稳定存储。诊断包在 stat/复制日志
// 之前调用它；日志之后仍可继续增长，但下载包只读取 stat 时已经存在的字节。
func Sync() error {
	watcherMu.Lock()
	defer watcherMu.Unlock()
	var result error
	for _, syncer := range activeLogSyncers {
		if err := syncer.Sync(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			result = errors.Join(result, err)
		}
	}
	return result
}

// openUniqueRunLog 为每次进程运行创建不可覆盖的独立日志文件。
// 纳秒时间、PID 和随机后缀共同避免“同一秒崩溃并重启”覆盖上一运行日志。
func openUniqueRunLog(dir string, now time.Time) (*os.File, string, error) {
	var lastPath string
	for attempt := 0; attempt < 8; attempt++ {
		var randomBytes [6]byte
		if _, err := rand.Read(randomBytes[:]); err != nil {
			// 极少数随机源不可用场景仍用 attempt 保证本次调用内不重复；
			// O_EXCL 是最终的不覆盖保证。
			randomBytes = [6]byte{}
			randomBytes[0] = byte(attempt)
		}
		runID := fmt.Sprintf(
			"run-%s-p%d-%s",
			now.UTC().Format("20060102T150405.000000000Z"),
			os.Getpid(),
			hex.EncodeToString(randomBytes[:]),
		)
		lastPath = filepath.Join(dir, runID+".log")
		file, err := os.OpenFile(lastPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return file, lastPath, nil
		}
		if !os.IsExist(err) {
			return nil, lastPath, err
		}
	}
	return nil, lastPath, fmt.Errorf("连续生成的日志文件名均已存在")
}

// dailyRotatingWriter 按“天”切分日志文件，文件名形如：<base>-YYYY-MM-DD.log
// 可选保留最近 N 天（retentionDays<=0 时不清理）。
type dailyRotatingWriter struct {
	dir           string
	base          string
	retentionDays int

	mu     sync.Mutex
	curDay string
	file   *os.File
	closed bool
}

func newDailyRotatingWriter(dir, base string, retentionDays int) *dailyRotatingWriter {
	w := &dailyRotatingWriter{dir: dir, base: base, retentionDays: retentionDays}
	_ = w.rotateIfNeededLocked(time.Now())
	return w
}

func (w *dailyRotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	if err := w.rotateIfNeededLocked(time.Now()); err != nil {
		return 0, err
	}
	if w.file == nil {
		return 0, io.ErrClosedPipe
	}
	return w.file.Write(p)
}

func (w *dailyRotatingWriter) rotateIfNeededLocked(now time.Time) error {
	if w.closed {
		return io.ErrClosedPipe
	}
	day := now.Format("2006-01-02")
	if w.file != nil && day == w.curDay {
		return nil
	}
	// 关闭旧文件
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	// 打开新文件
	name := w.filenameForDay(day)
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.file = f
	w.curDay = day
	// 清理过期文件
	w.cleanupLocked(now)
	return nil
}

func (w *dailyRotatingWriter) filenameForDay(day string) string {
	return filepath.Join(w.dir, w.base+"-"+day+".log")
}

func (w *dailyRotatingWriter) cleanupLocked(now time.Time) {
	if w.retentionDays <= 0 {
		return
	}
	cutoff := now.AddDate(0, 0, -w.retentionDays)
	pattern := filepath.Join(w.dir, w.base+"-*.log")
	files, _ := filepath.Glob(pattern)
	for _, f := range files {
		// 解析日期
		base := filepath.Base(f)
		// 期望格式：<base>-YYYY-MM-DD.log
		// 去掉前缀与后缀
		if !strings.HasPrefix(base, w.base+"-") || !strings.HasSuffix(base, ".log") {
			continue
		}
		dateStr := strings.TrimSuffix(strings.TrimPrefix(base, w.base+"-"), ".log")
		if t, err := time.Parse("2006-01-02", dateStr); err == nil {
			if t.Before(cutoff) {
				_ = os.Remove(f)
			}
		}
	}
}

// Close 关闭当前日志文件（实现 io.Closer 接口）
func (w *dailyRotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.file != nil {
		_ = w.file.Sync()
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

// Sync 将当前 daily 文件同步到稳定存储。
func (w *dailyRotatingWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		if w.closed {
			return io.ErrClosedPipe
		}
		return nil
	}
	return w.file.Sync()
}

// GetLogger 返回全局唯一的 logrus Logger。
// 便于在代码任意位置获取 Logger，而无需通过 instance 传递。
func GetLogger() *logrus.Logger {
	return logrus.StandardLogger()
}

// WithFields 是对全局 Logger 的便捷封装，返回带字段的 Entry。
func WithFields(fields logrus.Fields) *logrus.Entry {
	return logrus.StandardLogger().WithFields(fields)
}
