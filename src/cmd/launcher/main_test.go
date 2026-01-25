// Package main 提供 bililive-go 启动器测试
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bililive-go/bililive-go/src/pkg/ipc"
)

// TestCalculateSHA256 测试 SHA256 校验和计算
func TestCalculateSHA256(t *testing.T) {
	// 创建临时文件
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := []byte("hello world")

	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("无法创建测试文件: %v", err)
	}

	// 计算期望的 SHA256
	h := sha256.New()
	h.Write(testContent)
	expected := fmt.Sprintf("%x", h.Sum(nil))

	// 测试 calculateSHA256 函数
	actual, err := calculateSHA256(testFile)
	if err != nil {
		t.Fatalf("calculateSHA256 失败: %v", err)
	}

	if actual != expected {
		t.Errorf("SHA256 不匹配:\n期望: %s\n实际: %s", expected, actual)
	}
}

// TestCalculateSHA256_FileNotFound 测试文件不存在的情况
func TestCalculateSHA256_FileNotFound(t *testing.T) {
	_, err := calculateSHA256("/non/existent/file")
	if err == nil {
		t.Error("期望文件不存在时返回错误")
	}
}

// TestExtractTarGz 测试 .tar.gz 解压
func TestExtractTarGz(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建一个测试用的 .tar.gz 文件
	tarGzPath := filepath.Join(tmpDir, "test.tar.gz")
	binaryContent := []byte("#!/bin/bash\necho 'test binary'")
	createTarGz(t, tarGzPath, "bililive-go", binaryContent)

	// 解压目标
	dstPath := filepath.Join(tmpDir, "bililive-go")

	launcher := &Launcher{verbose: true}
	err := launcher.extractTarGz(tarGzPath, dstPath)
	if err != nil {
		t.Fatalf("extractTarGz 失败: %v", err)
	}

	// 验证文件内容
	content, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("无法读取解压后的文件: %v", err)
	}
	if string(content) != string(binaryContent) {
		t.Errorf("解压内容不匹配:\n期望: %s\n实际: %s", binaryContent, content)
	}
}

// TestExtractZip 测试 .zip 解压
func TestExtractZip(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建一个测试用的 .zip 文件
	zipPath := filepath.Join(tmpDir, "test.zip")
	binaryContent := []byte("test executable content")
	createZip(t, zipPath, "bililive-go.exe", binaryContent)

	// 解压目标
	dstPath := filepath.Join(tmpDir, "bililive-go.exe")

	launcher := &Launcher{verbose: true}
	err := launcher.extractZip(zipPath, dstPath)
	if err != nil {
		t.Fatalf("extractZip 失败: %v", err)
	}

	// 验证文件内容
	content, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("无法读取解压后的文件: %v", err)
	}
	if string(content) != string(binaryContent) {
		t.Errorf("解压内容不匹配:\n期望: %s\n实际: %s", binaryContent, content)
	}
}

// TestExtractUpdate_DirectCopy 测试直接复制可执行文件
func TestExtractUpdate_DirectCopy(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建源文件
	srcPath := filepath.Join(tmpDir, "bililive-go")
	srcContent := []byte("executable content")
	if err := os.WriteFile(srcPath, srcContent, 0755); err != nil {
		t.Fatalf("无法创建源文件: %v", err)
	}

	// 设置目标路径
	dstPath := filepath.Join(tmpDir, "output", "bililive-go")

	// 创建目标目录
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		t.Fatalf("无法创建目标目录: %v", err)
	}

	launcher := &Launcher{verbose: true}
	err := launcher.extractUpdate(srcPath, dstPath)
	if err != nil {
		t.Fatalf("extractUpdate 失败: %v", err)
	}

	// 验证文件内容
	content, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("无法读取复制后的文件: %v", err)
	}
	if string(content) != string(srcContent) {
		t.Errorf("复制内容不匹配")
	}
}

// TestCopyFile 测试文件复制
func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建源文件
	srcPath := filepath.Join(tmpDir, "source.txt")
	content := []byte("test content")
	if err := os.WriteFile(srcPath, content, 0755); err != nil {
		t.Fatalf("无法创建源文件: %v", err)
	}

	// 复制到目标
	dstPath := filepath.Join(tmpDir, "dest.txt")
	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile 失败: %v", err)
	}

	// 验证
	dstContent, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("无法读取目标文件: %v", err)
	}
	if string(dstContent) != string(content) {
		t.Errorf("复制内容不匹配")
	}

	// 验证权限保持
	srcInfo, _ := os.Stat(srcPath)
	dstInfo, _ := os.Stat(dstPath)
	if srcInfo.Mode() != dstInfo.Mode() {
		t.Errorf("文件权限不匹配: 源 %v, 目标 %v", srcInfo.Mode(), dstInfo.Mode())
	}
}

// TestLauncher_NewLauncher_DefaultConfig 测试默认配置
func TestLauncher_NewLauncher_DefaultConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建一个不存在的配置路径
	configPath := filepath.Join(tmpDir, "launcher-config.json")

	launcher, err := NewLauncher("test-instance", configPath)
	if err != nil {
		t.Fatalf("NewLauncher 失败: %v", err)
	}

	if launcher.instanceID != "test-instance" {
		t.Errorf("instanceID 不匹配: 期望 test-instance, 实际 %s", launcher.instanceID)
	}

	if launcher.config == nil {
		t.Error("config 不应为 nil")
	}
}

// TestPerformUpdate_Validation 测试更新执行时的验证
func TestPerformUpdate_SHA256Mismatch(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建下载文件
	downloadPath := filepath.Join(tmpDir, "update.exe")
	if err := os.WriteFile(downloadPath, []byte("fake binary"), 0755); err != nil {
		t.Fatalf("无法创建下载文件: %v", err)
	}

	// 创建 launcher
	launcher := &Launcher{
		config: &LauncherConfig{
			CurrentBinaryPath: filepath.Join(tmpDir, "current", "bililive-go"),
		},
		verbose: true,
		updateReq: &ipc.UpdateRequestPayload{
			NewVersion:     "v0.8.0",
			DownloadPath:   downloadPath,
			SHA256Checksum: "invalid_checksum_that_will_not_match",
		},
	}

	// 应该因为 SHA256 不匹配而失败
	err := launcher.performUpdate()
	if err == nil {
		t.Error("期望 SHA256 不匹配时返回错误")
	}
}

// TestPerformUpdate_FileNotExist 测试更新文件不存在
func TestPerformUpdate_FileNotExist(t *testing.T) {
	launcher := &Launcher{
		config:  &LauncherConfig{},
		verbose: true,
		updateReq: &ipc.UpdateRequestPayload{
			NewVersion:   "v0.8.0",
			DownloadPath: "/non/existent/file.exe",
		},
	}

	err := launcher.performUpdate()
	if err == nil {
		t.Error("期望文件不存在时返回错误")
	}
}

// =============================================================================
// 测试辅助函数
// =============================================================================

// createTarGz 创建一个包含单个文件的 .tar.gz 归档
func createTarGz(t *testing.T, archivePath, fileName string, content []byte) {
	t.Helper()

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("无法创建 tar.gz 文件: %v", err)
	}
	defer file.Close()

	gzWriter := gzip.NewWriter(file)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	header := &tar.Header{
		Name: fileName,
		Size: int64(len(content)),
		Mode: 0755,
	}

	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("无法写入 tar 头: %v", err)
	}

	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("无法写入 tar 内容: %v", err)
	}
}

// createZip 创建一个包含单个文件的 .zip 归档
func createZip(t *testing.T, archivePath, fileName string, content []byte) {
	t.Helper()

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("无法创建 zip 文件: %v", err)
	}
	defer file.Close()

	zipWriter := zip.NewWriter(file)
	defer zipWriter.Close()

	writer, err := zipWriter.Create(fileName)
	if err != nil {
		t.Fatalf("无法在 zip 中创建文件: %v", err)
	}

	if _, err := writer.Write(content); err != nil {
		t.Fatalf("无法写入 zip 内容: %v", err)
	}
}
