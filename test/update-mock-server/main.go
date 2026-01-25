// Package main 提供用于本地测试自动升级功能的 Mock 版本 API 服务器
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"
)

var (
	port      = flag.Int("port", 8888, "监听端口")
	version   = flag.String("version", "99.0.0", "模拟的最新版本号")
	changelog = flag.String("changelog", "", "更新日志（也可通过 MOCK_CHANGELOG 环境变量设置）")
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
		GitHub   string `json:"github"`
		Proxy    string `json:"proxy"`
		Filename string `json:"filename"`
		SHA256   string `json:"sha256"`
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

	http.HandleFunc("/api/versions", handleVersions)
	http.HandleFunc("/health", handleHealth)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("🚀 Mock 版本 API 服务器启动在 http://localhost%s", addr)
	log.Printf("   模拟版本: %s", *version)
	log.Printf("   更新日志: %s", *changelog)
	log.Printf("\n使用方式:")
	log.Printf("   GET http://localhost%s/api/versions?current=0.8.0&platform=%s-%s", addr, runtime.GOOS, runtime.GOARCH)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}

func handleVersions(w http.ResponseWriter, r *http.Request) {
	currentVersion := r.URL.Query().Get("current")
	platform := r.URL.Query().Get("platform")

	log.Printf("收到版本检查请求: current=%s, platform=%s", currentVersion, platform)

	// 确定文件名
	var filename string
	if platform != "" {
		if runtime.GOOS == "windows" {
			filename = fmt.Sprintf("bililive-%s.zip", platform)
		} else {
			filename = fmt.Sprintf("bililive-%s.tar.gz", platform)
		}
	} else {
		filename = "bililive-" + runtime.GOOS + "-" + runtime.GOARCH
		if runtime.GOOS == "windows" {
			filename += ".zip"
		} else {
			filename += ".tar.gz"
		}
	}

	// 构建模拟的 GitHub 下载链接
	githubURL := fmt.Sprintf("https://github.com/bililive-go/bililive-go/releases/download/v%s/%s", *version, filename)

	resp := VersionResponse{
		LatestVersion:   *version,
		ReleaseDate:     time.Now().Format("2006-01-02"),
		Changelog:       *changelog,
		Prerelease:      false,
		CurrentVersion:  currentVersion,
		UpdateAvailable: currentVersion != "" && currentVersion != *version,
		UpdateRequired:  false,
		Download: &struct {
			GitHub   string `json:"github"`
			Proxy    string `json:"proxy"`
			Filename string `json:"filename"`
			SHA256   string `json:"sha256"`
		}{
			GitHub:   githubURL,
			Proxy:    fmt.Sprintf("http://localhost:%d/mock-download?url=%s", *port, githubURL),
			Filename: filename,
			SHA256:   "0000000000000000000000000000000000000000000000000000000000000000", // 测试用
		},
		ReleasePage: fmt.Sprintf("https://github.com/bililive-go/bililive-go/releases/tag/v%s", *version),
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(resp)

	log.Printf("返回响应: update_available=%v, version=%s", resp.UpdateAvailable, resp.LatestVersion)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
