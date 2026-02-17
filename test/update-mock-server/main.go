// Package main 提供用于本地测试自动升级功能的 Mock 版本 API 服务器
//
// 本服务器模拟 bililive-go.com 的版本检测 API，支持完整的更新测试流程：
//   - 版本检测 API
//   - 更新包下载（自动将指定源文件打包）
//   - SHA256 校验和验证
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

var (
	port       = flag.Int("port", 8888, "监听端口")
	version    = flag.String("version", "99.0.0", "模拟的最新版本号")
	changelog  = flag.String("changelog", "", "更新日志（也可通过 MOCK_CHANGELOG 环境变量设置）")
	sourceFile = flag.String("source", "", "要打包的源二进制文件路径（默认使用 bin/bililive-dev 或 bin/bililive-dev.exe）")
)

// 打包后的更新信息
var (
	updatePackagePath   string
	updatePackageSHA256 string
	updatePackageSize   int64
	updateFilename      string
)

// VersionResponse 模拟 bililive-go.com/api/versions 的响应格式
type VersionResponse struct {
	LatestVersion   string `json:"latest_version"`
	ReleaseDate     string `json:"release_date"`
	Changelog       string `json:"changelog"`
	Prerelease      bool   `json:"prerelease"`
	CurrentVersion  string `json:"current_version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	UpdateRequired  bool   `json:"update_required"`
	Download        *struct {
		URLs     []string `json:"urls"` // 下载链接数组，按优先级排序
		Filename string   `json:"filename"`
		SHA256   string   `json:"sha256"`
		Size     int64    `json:"size"`
	} `json:"download,omitempty"`
	ReleasePage string `json:"release_page"`
}

func main() {
	flag.Parse()

	// 从环境变量读取 changelog
	if *changelog == "" {
		*changelog = os.Getenv("MOCK_CHANGELOG")
	}
	if *changelog == "" {
		*changelog = "这是本地测试的模拟更新"
	}

	// 准备更新包
	if err := prepareUpdatePackage(); err != nil {
		log.Fatalf("❌ 准备更新包失败: %v", err)
	}

	http.HandleFunc("/api/versions", handleVersions)
	http.HandleFunc("/download/", handleDownload)
	http.HandleFunc("/health", handleHealth)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("\n" + "═══════════════════════════════════════════════════════════════")
	log.Printf("  🚀 Mock 版本 API 服务器已启动")
	log.Printf("═══════════════════════════════════════════════════════════════")
	log.Printf("  监听地址:    http://localhost%s", addr)
	log.Printf("  模拟版本:    %s", *version)
	log.Printf("  更新日志:    %s", *changelog)
	log.Printf("  更新包路径:  %s", updatePackagePath)
	log.Printf("  更新包大小:  %.2f MB", float64(updatePackageSize)/1024/1024)
	log.Printf("  SHA256:      %s", updatePackageSHA256)
	log.Printf("───────────────────────────────────────────────────────────────")
	log.Printf("  测试 API:")
	log.Printf("    版本检测: GET http://localhost%s/api/versions?current=0.8.0&platform=%s-%s", addr, runtime.GOOS, runtime.GOARCH)
	log.Printf("    文件下载: GET http://localhost%s/download/%s", addr, updateFilename)
	log.Printf("═══════════════════════════════════════════════════════════════\n")

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}

// prepareUpdatePackage 准备更新包（将源文件打包成 zip）
func prepareUpdatePackage() error {
	// 确定源文件路径
	srcPath := *sourceFile
	if srcPath == "" {
		// 默认使用 bin/bililive-dev
		if runtime.GOOS == "windows" {
			srcPath = "bin/bililive-dev.exe"
		} else {
			srcPath = "bin/bililive-dev"
		}
	}

	// 检查源文件是否存在
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		return fmt.Errorf("源文件不存在: %s\n请先运行 make dev-incremental 构建开发版本", srcPath)
	}

	// 确定输出文件名
	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	updateFilename = fmt.Sprintf("bililive-%s.zip", platform)

	// 创建临时目录存放更新包
	tempDir, err := os.MkdirTemp("", "mock-update-*")
	if err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	updatePackagePath = filepath.Join(tempDir, updateFilename)

	log.Printf("正在创建更新包...")
	log.Printf("  源文件: %s", srcPath)
	log.Printf("  目标文件: %s", updatePackagePath)

	// 创建 zip 文件
	if err := createZipPackage(srcPath, updatePackagePath); err != nil {
		return fmt.Errorf("创建 zip 包失败: %w", err)
	}

	// 计算 SHA256
	hash, err := calculateSHA256(updatePackagePath)
	if err != nil {
		return fmt.Errorf("计算 SHA256 失败: %w", err)
	}
	updatePackageSHA256 = hash

	// 获取文件大小
	info, _ := os.Stat(updatePackagePath)
	updatePackageSize = info.Size()

	log.Printf("✅ 更新包准备完成")
	return nil
}

// createZipPackage 创建 zip 包
func createZipPackage(srcPath, dstPath string) error {
	zipFile, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// 确定 zip 内的文件名
	// 发布包中通常是 bililive-<platform>.exe 或 bililive-<platform>
	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	var innerName string
	if runtime.GOOS == "windows" {
		innerName = fmt.Sprintf("bililive-%s.exe", platform)
	} else {
		innerName = fmt.Sprintf("bililive-%s", platform)
	}

	// 添加文件到 zip
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(srcInfo)
	if err != nil {
		return err
	}
	header.Name = innerName
	header.Method = zip.Deflate

	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(writer, srcFile)
	return err
}

// calculateSHA256 计算文件的 SHA256 校验和
func calculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func handleVersions(w http.ResponseWriter, r *http.Request) {
	currentVersion := r.URL.Query().Get("current")
	platform := r.URL.Query().Get("platform")

	log.Printf("📥 收到版本检查请求: current=%s, platform=%s", currentVersion, platform)

	// 构建本地下载链接
	localDownloadURL := fmt.Sprintf("http://localhost:%d/download/%s", *port, updateFilename)

	// 模拟的 GitHub 下载链接（实际测试中不会使用）
	githubURL := fmt.Sprintf("https://github.com/bililive-go/bililive-go/releases/download/v%s/%s", *version, updateFilename)

	resp := VersionResponse{
		LatestVersion:   *version,
		ReleaseDate:     time.Now().Format("2006-01-02"),
		Changelog:       *changelog,
		Prerelease:      false,
		CurrentVersion:  currentVersion,
		UpdateAvailable: currentVersion != "" && currentVersion != *version,
		UpdateRequired:  false,
		Download: &struct {
			URLs     []string `json:"urls"`
			Filename string   `json:"filename"`
			SHA256   string   `json:"sha256"`
			Size     int64    `json:"size"`
		}{
			URLs:     []string{localDownloadURL, githubURL}, // 本地优先，GitHub 备用
			Filename: updateFilename,
			SHA256:   updatePackageSHA256,
			Size:     updatePackageSize,
		},
		ReleasePage: fmt.Sprintf("https://github.com/bililive-go/bililive-go/releases/tag/v%s", *version),
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(resp)

	log.Printf("📤 返回响应: update_available=%v, version=%s, sha256=%s", resp.UpdateAvailable, resp.LatestVersion, updatePackageSHA256[:16]+"...")
}

// handleDownload 处理更新包下载请求
func handleDownload(w http.ResponseWriter, r *http.Request) {
	log.Printf("📦 收到下载请求: %s", r.URL.Path)

	// 检查更新包是否存在
	if updatePackagePath == "" {
		http.Error(w, "更新包未准备", http.StatusServiceUnavailable)
		return
	}

	file, err := os.Open(updatePackagePath)
	if err != nil {
		http.Error(w, "无法打开更新包", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// 设置响应头
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", updateFilename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", updatePackageSize))

	// 发送文件
	written, err := io.Copy(w, file)
	if err != nil {
		log.Printf("❌ 发送文件失败: %v", err)
		return
	}

	log.Printf("✅ 文件发送完成: %d bytes", written)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
