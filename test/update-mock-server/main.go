// Package main 提供用于本地测试自动升级功能的 Mock 版本 API 服务器。
//
// 本服务器模拟 bililive-go.com 的版本检测 API，支持两种升级包来源：
//   - 自动将本地开发二进制打包为 zip；
//   - 直接提供已经构建好的 zip 或 tar.gz 升级包。
//
// 下载端点基于 http.ServeContent，支持 GET、HEAD 和 HTTP Range 断点续传。
package main

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const defaultEnvFile = "test/update-mock-server.env"

type serverConfig struct {
	EnvFile      string
	Host         string
	Port         int
	PublicHost   string
	Version      string
	Changelog    string
	Prerelease   bool
	AlwaysUpdate bool
	SourceFile   string
	PackageFile  string
}

type updatePackage struct {
	Path     string
	Filename string
	SHA256   string
	Size     int64
}

// DownloadInfo 描述版本 API 返回的升级包。
type DownloadInfo struct {
	URLs     []string `json:"urls"`
	Filename string   `json:"filename"`
	SHA256   string   `json:"sha256"`
	Size     int64    `json:"size"`
}

// VersionResponse 模拟 bililive-go.com/api/versions 的响应格式。
type VersionResponse struct {
	LatestVersion   string        `json:"latest_version"`
	ReleaseDate     string        `json:"release_date"`
	Changelog       string        `json:"changelog"`
	Prerelease      bool          `json:"prerelease"`
	CurrentVersion  string        `json:"current_version,omitempty"`
	UpdateAvailable bool          `json:"update_available"`
	UpdateRequired  bool          `json:"update_required"`
	Download        *DownloadInfo `json:"download,omitempty"`
	ReleasePage     string        `json:"release_page"`
}

type mockServer struct {
	config      serverConfig
	packageInfo updatePackage
	publicPort  int
}

func main() {
	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatal(err)
	}
}

func run() error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	cfg, err := parseConfig(os.Args[1:], repoRoot)
	if err != nil {
		return err
	}

	packageInfo, cleanup, err := prepareUpdatePackage(cfg, repoRoot)
	if err != nil {
		return fmt.Errorf("准备更新包失败: %w", err)
	}
	defer cleanup()

	bindAddress := net.JoinHostPort(stripIPv6Brackets(cfg.Host), strconv.Itoa(cfg.Port))
	listener, err := net.Listen("tcp", bindAddress)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", bindAddress, err)
	}
	defer listener.Close()

	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("无法识别监听地址: %s", listener.Addr())
	}

	mock := &mockServer{
		config:      cfg,
		packageInfo: packageInfo,
		publicPort:  tcpAddress.Port,
	}
	server := &http.Server{
		Handler:           mock.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	mock.logStartup(listener.Addr())
	if isLoopbackHost(cfg.Host) && !isLoopbackHost(cfg.PublicHost) {
		log.Printf("⚠️  当前仅监听环回地址 %q，但下载 URL 使用 %q；局域网/NAS 无法访问，请将 host 配置为 0.0.0.0 或 ::", cfg.Host, cfg.PublicHost)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("关闭服务器失败: %w", err)
		}
		log.Printf("Mock 版本 API 服务器已停止")
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("服务器运行失败: %w", err)
	}
}

func parseConfig(args []string, repoRoot string) (serverConfig, error) {
	envFile, envFileExplicit := findEnvFileArg(args, repoRoot)
	fileValues, err := loadEnvFile(envFile)
	if err != nil {
		if !envFileExplicit && errors.Is(err, os.ErrNotExist) {
			fileValues = map[string]string{}
		} else {
			return serverConfig{}, fmt.Errorf("读取变量文件 %s 失败: %w", envFile, err)
		}
	}

	configured := func(name, fallback string) string {
		if value, ok := os.LookupEnv(name); ok {
			return value
		}
		if value, ok := fileValues[name]; ok {
			return value
		}
		return fallback
	}

	port, err := parsePort(configured("BGO_UPDATE_PORT", "8888"))
	if err != nil {
		return serverConfig{}, fmt.Errorf("BGO_UPDATE_PORT 配置错误: %w", err)
	}
	prerelease, err := parseBoolConfig("BGO_UPDATE_PRERELEASE", configured("BGO_UPDATE_PRERELEASE", "false"))
	if err != nil {
		return serverConfig{}, err
	}
	alwaysUpdate, err := parseBoolConfig("BGO_UPDATE_ALWAYS_UPDATE", configured("BGO_UPDATE_ALWAYS_UPDATE", "false"))
	if err != nil {
		return serverConfig{}, err
	}

	changelog := configured("BGO_UPDATE_CHANGELOG", "")
	if changelog == "" {
		changelog = os.Getenv("MOCK_CHANGELOG") // 兼容原有本地调试配置。
	}

	cfg := serverConfig{}
	flags := flag.NewFlagSet("update-mock-server", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&cfg.EnvFile, "env-file", envFile, "变量文件路径")
	flags.StringVar(&cfg.Host, "host", configured("BGO_UPDATE_BIND_HOST", "127.0.0.1"), "监听地址")
	flags.IntVar(&cfg.Port, "port", port, "监听端口")
	flags.StringVar(&cfg.PublicHost, "public-host", configured("BGO_UPDATE_PUBLIC_HOST", "localhost"), "写入下载 URL、供容器或外部设备访问的主机名/IP")
	flags.StringVar(&cfg.Version, "version", configured("BGO_UPDATE_VERSION", "99.0.0"), "模拟的最新版本号")
	flags.StringVar(&cfg.Changelog, "changelog", changelog, "更新日志")
	flags.BoolVar(&cfg.Prerelease, "prerelease", prerelease, "是否将模拟版本标记为预发布版本")
	flags.BoolVar(&cfg.AlwaysUpdate, "always-update", alwaysUpdate, "是否忽略 current 参数并始终返回有更新")
	flags.StringVar(&cfg.SourceFile, "source", configured("BGO_UPDATE_SOURCE", ""), "要自动打包的源二进制文件路径")
	flags.StringVar(&cfg.PackageFile, "package", configured("BGO_UPDATE_PACKAGE", ""), "直接提供的已有 zip/tar.gz 升级包路径")
	if err := flags.Parse(args); err != nil {
		return serverConfig{}, err
	}
	if flags.NArg() != 0 {
		return serverConfig{}, fmt.Errorf("无法识别的位置参数: %s", strings.Join(flags.Args(), " "))
	}

	if cfg.Port < 1 || cfg.Port > 65535 {
		return serverConfig{}, fmt.Errorf("端口必须在 1 到 65535 之间，收到 %d", cfg.Port)
	}
	if strings.TrimSpace(cfg.Host) == "" {
		return serverConfig{}, fmt.Errorf("监听地址不能为空")
	}
	if strings.TrimSpace(cfg.PublicHost) == "" {
		return serverConfig{}, fmt.Errorf("公开主机名/IP 不能为空")
	}
	if cfg.SourceFile != "" && cfg.PackageFile != "" {
		return serverConfig{}, fmt.Errorf("source 与 package 不能同时设置")
	}
	if cfg.Changelog == "" {
		cfg.Changelog = "这是本地测试的模拟更新"
	}

	return cfg, nil
}

func findEnvFileArg(args []string, repoRoot string) (path string, explicit bool) {
	path, explicit = os.LookupEnv("BGO_UPDATE_ENV_FILE")
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-env-file" || args[i] == "--env-file":
			if i+1 < len(args) {
				path = args[i+1]
				explicit = true
			}
		case strings.HasPrefix(args[i], "-env-file="):
			path = strings.TrimPrefix(args[i], "-env-file=")
			explicit = true
		case strings.HasPrefix(args[i], "--env-file="):
			path = strings.TrimPrefix(args[i], "--env-file=")
			explicit = true
		}
	}
	if path == "" {
		path = defaultEnvFile
	}
	return resolveRepoPath(repoRoot, path), explicit
}

func loadEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("第 %d 行应使用 KEY=VALUE 格式", lineNumber)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == value[len(value)-1] && (value[0] == '\'' || value[0] == '"') {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("端口必须为整数，收到 %q", value)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("端口必须在 1 到 65535 之间，收到 %d", port)
	}
	return port, nil
}

func parseBoolConfig(name, value string) (bool, error) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s 配置必须为布尔值，收到 %q", name, value)
	}
	return parsed, nil
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("获取当前目录失败: %w", err)
	}
	for {
		if info, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil && !info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("无法从当前目录找到仓库根目录（缺少 go.mod）")
		}
		dir = parent
	}
}

func resolveRepoPath(repoRoot, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(repoRoot, filepath.Clean(path))
}

func prepareUpdatePackage(cfg serverConfig, repoRoot string) (updatePackage, func(), error) {
	packagePath := cfg.PackageFile
	cleanup := func() {}
	if packagePath != "" {
		packagePath = resolveRepoPath(repoRoot, packagePath)
	} else {
		sourcePath := cfg.SourceFile
		if sourcePath == "" {
			if runtime.GOOS == "windows" {
				sourcePath = "bin/bililive-dev.exe"
			} else {
				sourcePath = "bin/bililive-dev"
			}
		}
		sourcePath = resolveRepoPath(repoRoot, sourcePath)
		if info, err := os.Stat(sourcePath); err != nil || !info.Mode().IsRegular() {
			if err == nil {
				err = fmt.Errorf("不是普通文件")
			}
			return updatePackage{}, cleanup, fmt.Errorf("源文件不可用 %s: %w\n请先运行 make dev-incremental 构建开发版本", sourcePath, err)
		}

		tempDir, err := os.MkdirTemp("", "mock-update-*")
		if err != nil {
			return updatePackage{}, cleanup, fmt.Errorf("创建临时目录失败: %w", err)
		}
		cleanup = func() { _ = os.RemoveAll(tempDir) }
		platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
		packagePath = filepath.Join(tempDir, fmt.Sprintf("bililive-%s.zip", platform))
		log.Printf("正在创建更新包: %s -> %s", sourcePath, packagePath)
		if err := createZipPackage(sourcePath, packagePath); err != nil {
			cleanup()
			return updatePackage{}, func() {}, fmt.Errorf("创建 zip 包失败: %w", err)
		}
	}

	info, err := os.Stat(packagePath)
	if err != nil {
		cleanup()
		return updatePackage{}, func() {}, fmt.Errorf("升级包不可用 %s: %w", packagePath, err)
	}
	if !info.Mode().IsRegular() {
		cleanup()
		return updatePackage{}, func() {}, fmt.Errorf("升级包不是普通文件: %s", packagePath)
	}
	hash, err := calculateSHA256(packagePath)
	if err != nil {
		cleanup()
		return updatePackage{}, func() {}, fmt.Errorf("计算 SHA256 失败: %w", err)
	}

	return updatePackage{
		Path:     packagePath,
		Filename: filepath.Base(packagePath),
		SHA256:   hash,
		Size:     info.Size(),
	}, cleanup, nil
}

func createZipPackage(srcPath, dstPath string) error {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	zipFile, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	zipWriter := zip.NewWriter(zipFile)

	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	innerName := fmt.Sprintf("bililive-%s", platform)
	if runtime.GOOS == "windows" {
		innerName += ".exe"
	}
	header, err := zip.FileInfoHeader(srcInfo)
	if err == nil {
		header.Name = innerName
		header.Method = zip.Deflate
		var writer io.Writer
		writer, err = zipWriter.CreateHeader(header)
		if err == nil {
			_, err = io.Copy(writer, srcFile)
		}
	}
	if closeErr := zipWriter.Close(); err == nil {
		err = closeErr
	}
	if closeErr := zipFile.Close(); err == nil {
		err = closeErr
	}
	return err
}

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

func (s *mockServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/versions", s.handleVersions)
	mux.HandleFunc("/download/", s.handleDownload)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/", s.handleRoot)
	return mux
}

func (s *mockServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !allowReadMethod(w, r) {
		return
	}
	writeJSON(w, r, map[string]string{
		"service": "bgo-update-mock-server",
		"api":     "/api/versions",
	})
}

func (s *mockServer) handleVersions(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	currentVersion := r.URL.Query().Get("current")
	platform := r.URL.Query().Get("platform")
	log.Printf("📥 收到版本检查请求: current=%s, platform=%s", currentVersion, platform)

	resp := VersionResponse{
		LatestVersion:   s.config.Version,
		ReleaseDate:     time.Now().Format("2006-01-02"),
		Changelog:       s.config.Changelog,
		Prerelease:      s.config.Prerelease,
		CurrentVersion:  currentVersion,
		UpdateAvailable: s.config.AlwaysUpdate || (currentVersion != "" && currentVersion != s.config.Version),
		UpdateRequired:  false,
		Download: &DownloadInfo{
			URLs:     []string{s.packageURL()},
			Filename: s.packageInfo.Filename,
			SHA256:   s.packageInfo.SHA256,
			Size:     s.packageInfo.Size,
		},
		ReleasePage: fmt.Sprintf("https://github.com/bililive-go/bililive-go/releases/tag/v%s", s.config.Version),
	}
	writeJSON(w, r, resp)
	log.Printf("📤 返回响应: update_available=%v, version=%s, sha256=%s...", resp.UpdateAvailable, resp.LatestVersion, s.packageInfo.SHA256[:16])
}

func (s *mockServer) handleDownload(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	escapedPrefix := "/download/"
	if !strings.HasPrefix(r.URL.EscapedPath(), escapedPrefix) {
		http.NotFound(w, r)
		return
	}
	requestedName, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), escapedPrefix))
	if err != nil {
		http.Error(w, "升级包路径编码无效", http.StatusBadRequest)
		return
	}
	if requestedName != s.packageInfo.Filename {
		http.NotFound(w, r)
		return
	}

	file, err := os.Open(s.packageInfo.Path)
	if err != nil {
		http.Error(w, "无法打开更新包", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.Error(w, "无法读取更新包信息", http.StatusInternalServerError)
		return
	}

	if disposition := mime.FormatMediaType("attachment", map[string]string{"filename": s.packageInfo.Filename}); disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	http.ServeContent(w, r, s.packageInfo.Filename, info.ModTime(), file)
}

func (s *mockServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("OK"))
	}
}

func allowReadMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	return false
}

func writeJSON(w http.ResponseWriter, r *http.Request, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "JSON 编码失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func (s *mockServer) publicBaseURL() string {
	hostPort := net.JoinHostPort(stripIPv6Brackets(s.config.PublicHost), strconv.Itoa(s.publicPort))
	return "http://" + hostPort
}

func (s *mockServer) packageURL() string {
	return s.publicBaseURL() + "/download/" + url.PathEscape(s.packageInfo.Filename)
}

func (s *mockServer) logStartup(listenerAddress net.Addr) {
	log.Printf("\n" + "═══════════════════════════════════════════════════════════════")
	log.Printf("  🚀 Mock 版本 API 服务器已启动")
	log.Printf("═══════════════════════════════════════════════════════════════")
	log.Printf("  实际监听:      %s", listenerAddress)
	log.Printf("  版本检测 API:  %s/api/versions", s.publicBaseURL())
	log.Printf("  文件下载:      %s", s.packageURL())
	log.Printf("  模拟版本:      %s", s.config.Version)
	log.Printf("  更新日志:      %s", s.config.Changelog)
	log.Printf("  更新包路径:    %s", s.packageInfo.Path)
	log.Printf("  更新包大小:    %.2f MB", float64(s.packageInfo.Size)/1024/1024)
	log.Printf("  SHA256:        %s", s.packageInfo.SHA256)
	log.Printf("───────────────────────────────────────────────────────────────")
	log.Printf("  在被测 bgo 进程或容器中设置:")
	log.Printf("  VERSION_API_URL=%s/api/versions", s.publicBaseURL())
	log.Printf("═══════════════════════════════════════════════════════════════\n")
}

func stripIPv6Brackets(host string) string {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
}

func isLoopbackHost(host string) bool {
	host = stripIPv6Brackets(host)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
