package log

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
)

func TestOpenUniqueRunLogDoesNotOverwriteSameInstant(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, time.July, 30, 1, 2, 3, 4, time.UTC)

	first, firstPath, err := openUniqueRunLog(dir, now)
	if err != nil {
		t.Fatalf("创建第一份运行日志失败: %v", err)
	}
	if _, err := first.WriteString("first-run"); err != nil {
		t.Fatalf("写第一份运行日志失败: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("关闭第一份运行日志失败: %v", err)
	}

	second, secondPath, err := openUniqueRunLog(dir, now)
	if err != nil {
		t.Fatalf("创建第二份运行日志失败: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("关闭第二份运行日志失败: %v", err)
	}

	if firstPath == secondPath {
		t.Fatalf("同一时刻创建的两份运行日志发生碰撞: %s", firstPath)
	}
	content, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatalf("读取第一份运行日志失败: %v", err)
	}
	if string(content) != "first-run" {
		t.Fatalf("第一份运行日志被覆盖，实际内容为 %q", content)
	}
	if filepath.Dir(firstPath) != dir || filepath.Dir(secondPath) != dir {
		t.Fatal("运行日志创建到了意外目录")
	}
}

func TestDailyRotatingWriterDoesNotReopenAfterClose(t *testing.T) {
	writer := newDailyRotatingWriter(t.TempDir(), "bililive-go", 0)
	if _, err := writer.Write([]byte("before-close")); err != nil {
		t.Fatalf("关闭前写入失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("重复关闭应幂等，实际错误: %v", err)
	}
	if _, err := writer.Write([]byte("after-close")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("关闭后写入应返回 io.ErrClosedPipe，实际为 %v", err)
	}
}

func TestNewDoesNotDeletePreviousRunLogs(t *testing.T) {
	dir := t.TempDir()
	previous := filepath.Join(dir, "bililive-go-2000-01-01.log")
	if err := os.WriteFile(previous, []byte("previous-crash-evidence"), 0o600); err != nil {
		t.Fatalf("创建上一运行日志失败: %v", err)
	}
	configs.SetCurrentConfig(&configs.Config{
		Log: configs.Log{
			OutPutFolder: dir,
			SaveLastLog:  true,
			RotateDays:   0,
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	_ = New(ctx)
	cancel()
	Close()

	content, err := os.ReadFile(previous)
	if err != nil {
		t.Fatalf("新启动删除了上一运行日志: %v", err)
	}
	if string(content) != "previous-crash-evidence" {
		t.Fatalf("上一运行日志被改写: %q", content)
	}
}

func TestSyncFlushesCurrentGrowingLogWithoutClosingIt(t *testing.T) {
	dir := t.TempDir()
	configs.SetCurrentConfig(&configs.Config{
		Log: configs.Log{
			OutPutFolder: dir,
			SaveLastLog:  true,
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	_ = New(ctx)
	defer func() {
		cancel()
		Close()
	}()

	GetLogger().Info("diagnostic-log-snapshot-before")
	if err := Sync(); err != nil {
		t.Fatalf("同步增长中的日志失败: %v", err)
	}
	path := filepath.Join(dir, "bililive-go-"+time.Now().Format("2006-01-02")+".log")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取同步后的日志失败: %v", err)
	}
	if !strings.Contains(string(content), "diagnostic-log-snapshot-before") {
		t.Fatalf("同步后的日志缺少刚写入内容: %q", content)
	}

	// Sync 只是冻结下载边界，不能把 writer 关闭。
	GetLogger().Info("diagnostic-log-snapshot-after")
	if err := Sync(); err != nil {
		t.Fatalf("第二次同步增长中的日志失败: %v", err)
	}
	content, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取第二次同步后的日志失败: %v", err)
	}
	if !strings.Contains(string(content), "diagnostic-log-snapshot-after") {
		t.Fatalf("Sync 意外关闭了当前日志 writer: %q", content)
	}
}

func TestBuildSnapshotArchiveCopiesFixedTailAndFiltersUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(logDir, "bililive-go-2026-07-30.log")
	if err := os.WriteFile(source, []byte("0123456789ABCDEFGHIJ"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "unrelated.log"), []byte("secret-unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	configs.SetCurrentConfig(&configs.Config{
		AppDataPath: root,
		Log: configs.Log{
			OutPutFolder: logDir,
		},
	})

	artifact, err := BuildSnapshotArchive(2, 10)
	if err != nil {
		t.Fatalf("创建日志快照失败: %v", err)
	}
	defer os.Remove(artifact.Path)
	if err := os.WriteFile(source, []byte("0123456789ABCDEFGHIJ-after"), 0o600); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	entries := map[string]string{}
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		content, readErr := io.ReadAll(reader)
		if readErr != nil {
			t.Fatal(readErr)
		}
		entries[header.Name] = string(content)
	}
	if got := entries["logs/bililive-go-2026-07-30.log"]; got != "ABCDEFGHIJ" {
		t.Fatalf("快照没有固定为最初 size 的最后 10 字节: %q", got)
	}
	if _, exists := entries["logs/unrelated.log"]; exists {
		t.Fatal("日志快照包含了无关 .log 文件")
	}
	manifest := entries["manifest.json"]
	if !strings.Contains(manifest, `"truncated_head": true`) {
		t.Fatalf("manifest 未说明头部截断: %s", manifest)
	}
	if strings.Contains(manifest, root) {
		t.Fatalf("manifest 泄露了服务端绝对路径: %s", manifest)
	}
}

func TestLogSnapshotQueueHonorsCanceledContext(t *testing.T) {
	<-snapshotExportGate
	released := false
	defer func() {
		if !released {
			snapshotExportGate <- struct{}{}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := BuildSnapshotArchiveContext(ctx, 1, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消的日志导出未立即退出: %v", err)
	}

	snapshotExportGate <- struct{}{}
	released = true
}
