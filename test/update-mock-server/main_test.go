package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseConfigPrecedence(t *testing.T) {
	repoRoot := t.TempDir()
	envFile := filepath.Join(repoRoot, "mock.env")
	err := os.WriteFile(envFile, []byte(strings.Join([]string{
		"BGO_UPDATE_BIND_HOST=127.0.0.2",
		"BGO_UPDATE_PORT=9001",
		"BGO_UPDATE_PUBLIC_HOST=file.example",
		"BGO_UPDATE_VERSION=1.0.0-file",
		"BGO_UPDATE_PACKAGE=dist/file.tar.gz",
		"BGO_UPDATE_PRERELEASE=true",
		"BGO_UPDATE_ALWAYS_UPDATE=true",
	}, "\n")), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("BGO_UPDATE_PORT", "9002")
	t.Setenv("BGO_UPDATE_VERSION", "2.0.0-env")
	cfg, err := parseConfig([]string{
		"-env-file", envFile,
		"-port", "9003",
		"-version", "3.0.0-cli",
	}, repoRoot)
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}

	if cfg.Host != "127.0.0.2" {
		t.Fatalf("变量文件中的 host 未生效: %q", cfg.Host)
	}
	if cfg.Port != 9003 || cfg.Version != "3.0.0-cli" {
		t.Fatalf("命令行未覆盖环境配置: port=%d version=%q", cfg.Port, cfg.Version)
	}
	if cfg.PublicHost != "file.example" || cfg.PackageFile != "dist/file.tar.gz" {
		t.Fatalf("变量文件配置不完整: publicHost=%q package=%q", cfg.PublicHost, cfg.PackageFile)
	}
	if !cfg.Prerelease || !cfg.AlwaysUpdate {
		t.Fatalf("布尔配置未正确解析: prerelease=%v alwaysUpdate=%v", cfg.Prerelease, cfg.AlwaysUpdate)
	}
}

func TestParseConfigRejectsInvalidEnvironmentPort(t *testing.T) {
	repoRoot := t.TempDir()
	t.Setenv("BGO_UPDATE_PORT", "not-a-port")
	_, err := parseConfig(nil, repoRoot)
	if err == nil || !strings.Contains(err.Error(), "BGO_UPDATE_PORT") {
		t.Fatalf("无效环境端口应返回清晰错误，实际为 %v", err)
	}
}

func TestPrepareExistingPackageUsesRepoRelativePath(t *testing.T) {
	repoRoot := t.TempDir()
	relativePath := filepath.Join("dist", "升级包 #1?.tar.gz")
	absolutePath := filepath.Join(repoRoot, relativePath)
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("0123456789")
	if err := os.WriteFile(absolutePath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	packageInfo, cleanup, err := prepareUpdatePackage(serverConfig{PackageFile: relativePath}, repoRoot)
	if err != nil {
		t.Fatalf("准备已有升级包失败: %v", err)
	}
	defer cleanup()

	expectedHash := sha256.Sum256(content)
	if packageInfo.Path != absolutePath {
		t.Fatalf("升级包路径未相对仓库根目录解析: %q", packageInfo.Path)
	}
	if packageInfo.Filename != filepath.Base(relativePath) || packageInfo.Size != int64(len(content)) {
		t.Fatalf("升级包元信息错误: %+v", packageInfo)
	}
	if packageInfo.SHA256 != hex.EncodeToString(expectedHash[:]) {
		t.Fatalf("SHA256 错误: %s", packageInfo.SHA256)
	}
}

func TestMockServerSupportsIPv6URLSpecialFilenameHeadAndRange(t *testing.T) {
	tempDir := t.TempDir()
	filename := "升级包 #1?.tar.gz"
	packagePath := filepath.Join(tempDir, filename)
	content := []byte("0123456789")
	if err := os.WriteFile(packagePath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	mock := &mockServer{
		config: serverConfig{
			PublicHost:   "2001:db8::1",
			Version:      "1.2.3-local",
			Changelog:    "测试",
			Prerelease:   true,
			AlwaysUpdate: true,
		},
		packageInfo: updatePackage{
			Path:     packagePath,
			Filename: filename,
			SHA256:   strings.Repeat("a", 64),
			Size:     int64(len(content)),
		},
		publicPort: 8099,
	}
	server := httptest.NewServer(mock.routes())
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/versions?current=1.2.3-local")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var versionResponse VersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&versionResponse); err != nil {
		t.Fatal(err)
	}
	expectedURL := "http://[2001:db8::1]:8099/download/" + url.PathEscape(filename)
	if len(versionResponse.Download.URLs) != 1 || versionResponse.Download.URLs[0] != expectedURL {
		t.Fatalf("下载 URL 未正确处理 IPv6 或特殊文件名: %#v", versionResponse.Download.URLs)
	}
	if !versionResponse.UpdateAvailable || !versionResponse.Prerelease {
		t.Fatalf("版本响应标志错误: %+v", versionResponse)
	}

	downloadURL := server.URL + "/download/" + url.PathEscape(filename)
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-999")
	downloadResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer downloadResp.Body.Close()
	downloaded, err := io.ReadAll(downloadResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if downloadResp.StatusCode != http.StatusPartialContent {
		t.Fatalf("超出末尾的有效 Range 应返回 206，实际为 %d", downloadResp.StatusCode)
	}
	if contentType := downloadResp.Header.Get("Content-Type"); contentType != "application/gzip" {
		t.Fatalf("tar.gz 应使用 application/gzip，实际为 %q", contentType)
	}
	if got := downloadResp.Header.Get("Content-Range"); got != "bytes 0-9/10" {
		t.Fatalf("Content-Range 错误: %q", got)
	}
	if string(downloaded) != string(content) {
		t.Fatalf("Range 下载内容错误: %q", downloaded)
	}

	headReq, err := http.NewRequest(http.MethodHead, server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	headResp, err := http.DefaultClient.Do(headReq)
	if err != nil {
		t.Fatal(err)
	}
	defer headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD / 应与 GET / 一致，实际为 %d", headResp.StatusCode)
	}
	body, err := io.ReadAll(headResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("HEAD 响应不应包含 body: %q", body)
	}

	zipFilename := "bililive-linux-amd64.zip"
	zipPath := filepath.Join(tempDir, zipFilename)
	if err := os.WriteFile(zipPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	mock.packageInfo.Path = zipPath
	mock.packageInfo.Filename = zipFilename
	zipResp, err := http.Get(server.URL + "/download/" + zipFilename)
	if err != nil {
		t.Fatal(err)
	}
	defer zipResp.Body.Close()
	if contentType := zipResp.Header.Get("Content-Type"); contentType != "application/zip" {
		t.Fatalf("zip 应使用 application/zip，实际为 %q", contentType)
	}
}

func TestPrepareSourceCreatesZip(t *testing.T) {
	repoRoot := t.TempDir()
	sourcePath := filepath.Join(repoRoot, "bin", "bililive-dev")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	packageInfo, cleanup, err := prepareUpdatePackage(serverConfig{SourceFile: sourcePath}, repoRoot)
	if err != nil {
		t.Fatalf("自动打包失败: %v", err)
	}
	defer cleanup()
	if filepath.Ext(packageInfo.Path) != ".zip" || packageInfo.Size == 0 {
		t.Fatalf("自动打包结果错误: %+v", packageInfo)
	}

	reader, err := zip.OpenReader(packageInfo.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if len(reader.File) != 1 {
		t.Fatalf("zip 内文件数错误: %d", len(reader.File))
	}
	expectedName := "bililive-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		expectedName += ".exe"
	}
	if reader.File[0].Name != expectedName {
		t.Fatalf("zip 内二进制文件名错误: %q", reader.File[0].Name)
	}
}

func TestLoopbackHostDetection(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "::1", "[::1]"} {
		if !isLoopbackHost(host) {
			t.Fatalf("应识别为环回地址: %s", host)
		}
	}
	for _, host := range []string{"0.0.0.0", "::", "192.0.2.10", "dev.example"} {
		if isLoopbackHost(host) {
			t.Fatalf("不应识别为环回地址: %s", host)
		}
	}
}
