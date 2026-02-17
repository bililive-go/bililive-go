package log

import (
	"context"
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
	if _, err := os.Stat(outputFolder); os.IsNotExist(err) {
		log.Fatalf("err: \"%s\", Failed to determine log output folder: %s", err, outputFolder)
	} else {
		if config.Log.SaveEveryLog {
			runID := time.Now().Format("run-2006-01-02-15-04-05")
			logLocation := filepath.Join(outputFolder, runID+".log")
			logFile, err := os.OpenFile(logLocation, os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				log.Fatalf("Failed to open log file %s for output: %s", logLocation, err)
			} else {
				writers = append(writers, logFile)
				closers = append(closers, logFile)
			}
		}
		if config.Log.SaveLastLog {
			// 由 Launcher 启动时（版本切换）不清理旧日志
			// 因为前一版本的进程可能仍持有日志文件的写入句柄
			// 在 Linux/Docker 上删除会导致前一版本的最后日志丢失
			if !isLauncherManaged {
				purgePattern := filepath.Join(outputFolder, "bililive-go-*.log")
				matches, _ := filepath.Glob(purgePattern)
				for _, f := range matches {
					_ = os.Remove(f)
				}
			}
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
	}
	watcherCtx, cancel := context.WithCancel(ctx)
	stopDebugWatcher = cancel
	watcherMu.Unlock()

	bilisentry.GoWithContext(watcherCtx, func(ctx context.Context) {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		prev := config.Debug
		for {
			select {
			case <-ctx.Done():
				// context 取消时关闭所有日志文件句柄
				for _, c := range closers {
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

// dailyRotatingWriter 按“天”切分日志文件，文件名形如：<base>-YYYY-MM-DD.log
// 可选保留最近 N 天（retentionDays<=0 时不清理）。
type dailyRotatingWriter struct {
	dir           string
	base          string
	retentionDays int

	mu     sync.Mutex
	curDay string
	file   *os.File
}

func newDailyRotatingWriter(dir, base string, retentionDays int) *dailyRotatingWriter {
	w := &dailyRotatingWriter{dir: dir, base: base, retentionDays: retentionDays}
	_ = w.rotateIfNeededLocked(time.Now())
	return w
}

func (w *dailyRotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.rotateIfNeededLocked(time.Now()); err != nil {
		return 0, err
	}
	if w.file == nil {
		return 0, io.ErrClosedPipe
	}
	return w.file.Write(p)
}

func (w *dailyRotatingWriter) rotateIfNeededLocked(now time.Time) error {
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
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
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
