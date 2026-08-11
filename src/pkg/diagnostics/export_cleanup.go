package diagnostics

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// diagnostics 导出文件只用于一次 HTTP 响应。正常响应结束会立即删除；这个
// 保留窗口只保护共享 AppData 中另一个仍在发送文件的实例，同时清理进程被
// SIGKILL 后遗留的完整导出和 atomicWriteFile 临时文件。
const staleExportRetention = time.Hour

func cleanupStaleExports(dir string, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := now.Add(-staleExportRetention)
	var result error
	removed := false
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "bililive-go-") &&
			!strings.HasPrefix(name, ".bililive-go-") {
			continue
		}
		path := filepath.Join(dir, name)
		info, infoErr := os.Lstat(path)
		if errors.Is(infoErr, fs.ErrNotExist) {
			continue
		}
		if infoErr != nil {
			result = errors.Join(result, infoErr)
			continue
		}
		// 绝不递归删除目录。符号链接本身可以安全删除，且不能通过它清理
		// diagnostics 根目录之外的内容。
		if info.IsDir() || !info.ModTime().Before(cutoff) {
			continue
		}
		if removeErr := os.Remove(path); removeErr != nil &&
			!errors.Is(removeErr, fs.ErrNotExist) {
			result = errors.Join(result, removeErr)
			continue
		}
		removed = true
	}
	if removed {
		result = errors.Join(result, syncDir(dir))
	}
	return result
}
