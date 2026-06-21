// ffmpeg-mock-server 是一个用于 e2e 测试的模拟 FFmpeg 下载服务器。
//
// 它在启动时编译 test/fake-ffmpeg，将其打包成 zip 压缩包，
// 并通过 HTTP 提供下载，支持速度限制和失败模拟以测试各种场景。
//
// API:
//
//	GET /health      — 健康检查（等待 zip 准备好再返回 200）
//	GET /ffmpeg.zip  — 下载 fake-ffmpeg zip（?speed=<字节/秒>, ?fail=true）
//	POST /control    — 运行时修改行为 {"speed": N, "fail": true/false}
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

var (
	port = flag.Int("port", 8890, "监听端口")

	// 全局控制状态
	globalSpeedBytesPerSec atomic.Int64 // 0 = 不限速
	globalFail             atomic.Bool

	zipData     []byte
	zipReady    = make(chan struct{})
	zipBuildErr error
)

func main() {
	flag.Parse()

	go buildZip()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/ffmpeg.zip", handleDownload)
	mux.HandleFunc("/control", handleControl)

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	log.Printf("[ffmpeg-mock] 启动服务器: http://%s", addr)

	server := &http.Server{Addr: addr, Handler: mux}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[ffmpeg-mock] 服务器错误: %v", err)
	}
}

func buildZip() {
	defer close(zipReady)
	data, err := buildFakeFfmpegZip()
	if err != nil {
		zipBuildErr = err
		log.Printf("[ffmpeg-mock] 构建 fake-ffmpeg zip 失败: %v", err)
		return
	}
	zipData = data
	log.Printf("[ffmpeg-mock] fake-ffmpeg zip 已就绪，大小: %d 字节", len(data))
}

// handleHealth 等待 zip 就绪后返回 200，供 Playwright 做启动检查
func handleHealth(w http.ResponseWriter, r *http.Request) {
	select {
	case <-zipReady:
	case <-time.After(120 * time.Second):
		http.Error(w, "zip build timeout", http.StatusServiceUnavailable)
		return
	}
	if zipBuildErr != nil {
		http.Error(w, "zip build failed: "+zipBuildErr.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

// handleDownload 提供 fake-ffmpeg zip 下载，支持速度限制和失败模拟
func handleDownload(w http.ResponseWriter, r *http.Request) {
	<-zipReady
	if zipBuildErr != nil {
		http.Error(w, "zip not available: "+zipBuildErr.Error(), http.StatusInternalServerError)
		return
	}

	speed := globalSpeedBytesPerSec.Load()
	fail := globalFail.Load()

	if s := r.URL.Query().Get("speed"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			speed = v
		}
	}
	if r.URL.Query().Get("fail") == "true" {
		fail = true
	}

	if fail {
		// 返回 500 模拟下载失败，让 remotetools Install() 报错
		http.Error(w, "simulated download failure", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="ffmpeg.zip"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(zipData)))
	w.WriteHeader(http.StatusOK)

	if speed <= 0 {
		w.Write(zipData)
		return
	}

	// 限速发送
	const chunkSize = 4096
	data := zipData
	for len(data) > 0 {
		n := chunkSize
		if n > len(data) {
			n = len(data)
		}
		w.Write(data[:n])
		data = data[n:]
		if flusher, ok2 := w.(http.Flusher); ok2 {
			flusher.Flush()
		}
		if len(data) > 0 {
			delay := time.Duration(float64(n) / float64(speed) * float64(time.Second))
			time.Sleep(delay)
		}
	}
}

type controlRequest struct {
	Speed *int64 `json:"speed"` // nil = 不修改
	Fail  *bool  `json:"fail"`  // nil = 不修改
}

func handleControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST", http.StatusMethodNotAllowed)
		return
	}
	var req controlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Speed != nil {
		globalSpeedBytesPerSec.Store(*req.Speed)
	}
	if req.Fail != nil {
		globalFail.Store(*req.Fail)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(fmt.Sprintf(`{"speed":%d,"fail":%v}`,
		globalSpeedBytesPerSec.Load(), globalFail.Load())))
}

// buildFakeFfmpegZip 编译 test/fake-ffmpeg 并打包成 zip
func buildFakeFfmpegZip() ([]byte, error) {
	// 支持从项目根或 test/ffmpeg-mock-server 目录运行
	fakeFfmpegPkg := "./test/fake-ffmpeg"
	if _, err := os.Stat("test/fake-ffmpeg"); os.IsNotExist(err) {
		fakeFfmpegPkg = "../fake-ffmpeg"
	}

	tmpDir, err := os.MkdirTemp("", "ffmpeg-mock-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	binaryName := "ffmpeg"
	if runtime.GOOS == "windows" {
		binaryName = "ffmpeg.exe"
	}
	outBinary := filepath.Join(tmpDir, binaryName)

	args := []string{"build", "-o", outBinary, fakeFfmpegPkg}
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	log.Printf("[ffmpeg-mock] 编译 fake-ffmpeg: go %v", args)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("编译 fake-ffmpeg 失败: %w", err)
	}

	binaryData, err := os.ReadFile(outBinary)
	if err != nil {
		return nil, fmt.Errorf("读取编译产物失败: %w", err)
	}

	// zip 中的路径和 remotetools config 中的 pathToEntry 保持一致
	entryPath := "bin/ffmpeg"
	if runtime.GOOS == "windows" {
		entryPath = "bin/ffmpeg.exe"
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	header := &zip.FileHeader{
		Name:   entryPath,
		Method: zip.Deflate,
	}
	header.SetMode(0o755)
	fw, err := zw.CreateHeader(header)
	if err != nil {
		return nil, fmt.Errorf("创建 zip 条目失败: %w", err)
	}
	if _, err = fw.Write(binaryData); err != nil {
		return nil, fmt.Errorf("写入 zip 条目失败: %w", err)
	}
	if err = zw.Close(); err != nil {
		return nil, fmt.Errorf("关闭 zip 写入器失败: %w", err)
	}

	return buf.Bytes(), nil
}
