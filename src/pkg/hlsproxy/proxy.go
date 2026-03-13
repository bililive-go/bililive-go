package hlsproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	applog "github.com/bililive-go/bililive-go/src/log"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

type Proxy struct {
	listener         net.Listener
	server           *http.Server
	localURL         *url.URL
	upstreamURL      *url.URL
	headers          map[string]string
	filterPreloading bool
}

// New 创建一个 HLS 本地代理。
// 当前主要用于 Soop 的 m3u8 兼容处理：
// - 重写媒体分段 URL 到本地代理；
// - 可选过滤名称中包含 preloading 的分段。
func New(upstreamURL *url.URL, headers map[string]string, filterPreloading bool) (*Proxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to create hls proxy listener: %w", err)
	}

	localURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d/stream.m3u8", listener.Addr().(*net.TCPAddr).Port))
	if err != nil {
		listener.Close()
		return nil, err
	}

	proxy := &Proxy{
		listener:         listener,
		localURL:         localURL,
		upstreamURL:      upstreamURL,
		headers:          headers,
		filterPreloading: filterPreloading,
	}
	applog.GetLogger().Debugf("HLS 代理已创建: upstream=%s local=%s filterPreloading=%v", upstreamURL.String(), localURL.String(), filterPreloading)
	return proxy, nil
}

// LocalURL 返回本地代理入口地址，供下载器直接消费。
func (p *Proxy) LocalURL() *url.URL {
	return p.localURL
}

// Start 启动本地 HLS 代理。
// 代理同时处理：
// - 播放列表请求 `/stream.m3u8`
// - 媒体分段代理 `/media?url=...`
func (p *Proxy) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/stream.m3u8", p.handlePlaylist)
	mux.HandleFunc("/media", p.handleMedia)

	p.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
	}

	bilisentry.GoWithContext(ctx, func(ctx context.Context) {
		<-ctx.Done()
		_ = p.Stop()
	})

	go func() {
		_ = p.server.Serve(p.listener)
	}()
	applog.GetLogger().Debugf("HLS 代理已启动: local=%s upstream=%s", p.localURL.String(), p.upstreamURL.String())
	return nil
}

// Stop 停止本地 HLS 代理并释放监听端口。
func (p *Proxy) Stop() error {
	applog.GetLogger().Debugf("HLS 代理停止: local=%s", p.localURL.String())
	if p.server != nil {
		_ = p.server.Close()
	}
	if p.listener != nil {
		return p.listener.Close()
	}
	return nil
}

func (p *Proxy) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	p.serveTargetPlaylist(w, r, p.upstreamURL)
}

func (p *Proxy) handleMedia(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("url")
	if target == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}

	targetURL, err := url.Parse(target)
	if err != nil {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}

	if looksLikePlaylist(targetURL.String()) {
		p.serveTargetPlaylist(w, r, targetURL)
		return
	}

	p.proxyRaw(w, r, targetURL)
}

func (p *Proxy) serveTargetPlaylist(w http.ResponseWriter, r *http.Request, targetURL *url.URL) {
	body, contentType, statusCode, err := p.fetchTarget(r.Context(), targetURL)
	if err != nil {
		http.Error(w, "获取上游 HLS 播放列表失败", http.StatusBadGateway)
		return
	}
	if statusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("上游 HLS 播放列表返回异常状态码: %d", statusCode), statusCode)
		return
	}

	content := string(body)
	rewritten, err := rewritePlaylist(content, targetURL, p.localURL, p.filterPreloading)
	if err != nil {
		http.Error(w, "重写 HLS 播放列表失败", http.StatusInternalServerError)
		return
	}
	applog.GetLogger().Debugf("HLS 播放列表已重写: target=%s local=%s bytes=%d", targetURL.String(), p.localURL.String(), len(rewritten))

	if contentType == "" {
		contentType = "application/vnd.apple.mpegurl"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, rewritten)
}

func (p *Proxy) proxyRaw(w http.ResponseWriter, r *http.Request, targetURL *url.URL) {
	resp, err := p.doUpstreamRequest(r.Context(), targetURL)
	if err != nil {
		http.Error(w, "获取上游 HLS 资源失败", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil && r.Context().Err() == nil {
		applog.GetLogger().Warnf("HLS 原始资源透传失败: target=%s err=%v", targetURL.String(), err)
	}
}

// fetchTarget 透传请求到上游 m3u8 / ts / m4s / init 段。
func (p *Proxy) fetchTarget(ctx context.Context, targetURL *url.URL) ([]byte, string, int, error) {
	resp, err := p.doUpstreamRequest(ctx, targetURL)
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", 0, fmt.Errorf("读取 HLS 上游响应失败: %w", err)
	}

	return body, resp.Header.Get("Content-Type"), resp.StatusCode, nil
}

func (p *Proxy) doUpstreamRequest(ctx context.Context, targetURL *url.URL) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建 HLS 代理上游请求失败: %w", err)
	}

	for key, value := range p.headers {
		req.Header.Set(key, value)
	}

	client := utils.CreateDownloadClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 HLS 上游资源失败: %w", err)
	}
	return resp, nil
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// rewritePlaylist 重写 m3u8 内容。
// 核心职责：
// 1. 把分段 URL 重写到本地代理；
// 2. 把 EXT-X-MAP 中的 URI 也重写到本地代理；
// 3. 在需要时过滤掉 Soop 的 preloading 分片，并同步丢弃其前置标签。
func rewritePlaylist(content string, upstreamURL, localBaseURL *url.URL, filterPreloading bool) (string, error) {
	lines := strings.Split(content, "\n")
	output := make([]string, 0, len(lines))
	pending := make([]string, 0, 4)
	filteredSegments := 0

	flushPending := func() {
		if len(pending) == 0 {
			return
		}
		output = append(output, pending...)
		pending = pending[:0]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			rewrittenTag := rewriteTagURI(trimmed, upstreamURL, localBaseURL)
			if isURIRelatedTag(trimmed) {
				pending = append(pending, rewrittenTag)
			} else {
				flushPending()
				output = append(output, rewrittenTag)
			}
			continue
		}

		absURL, err := upstreamURL.Parse(trimmed)
		if err != nil {
			return "", fmt.Errorf("解析 HLS 分段相对地址失败: %w", err)
		}
		if filterPreloading && strings.Contains(absURL.String(), "preloading") {
			// preloading 片段对应的 EXTINF / 标签也一并丢弃，避免出现“标签存在但媒体 URI 被删掉”的非法 m3u8。
			pending = pending[:0]
			filteredSegments++
			continue
		}

		flushPending()
		output = append(output, buildLocalMediaURL(localBaseURL, absURL.String()))
	}

	flushPending()
	applog.GetLogger().Debugf("HLS 播放列表重写完成: upstream=%s filteredPreloading=%d outputLines=%d", upstreamURL.String(), filteredSegments, len(output))
	return strings.Join(output, "\n") + "\n", nil
}

// buildLocalMediaURL 将上游媒体地址映射到本地代理的 /media 路径。
func buildLocalMediaURL(localBaseURL *url.URL, target string) string {
	u := *localBaseURL
	u.Path = "/media"
	q := u.Query()
	q.Set("url", target)
	u.RawQuery = q.Encode()
	return u.String()
}

// rewriteTagURI 重写 EXT-X-MAP 等标签中的 URI 属性。
func rewriteTagURI(line string, upstreamURL, localBaseURL *url.URL) string {
	if !strings.Contains(line, `URI="`) {
		return line
	}

	start := strings.Index(line, `URI="`)
	if start < 0 {
		return line
	}
	valueStart := start + len(`URI="`)
	end := strings.Index(line[valueStart:], `"`)
	if end < 0 {
		return line
	}

	rawURI := line[valueStart : valueStart+end]
	absURL, err := upstreamURL.Parse(rawURI)
	if err != nil {
		return line
	}

	return line[:valueStart] + buildLocalMediaURL(localBaseURL, absURL.String()) + line[valueStart+end:]
}

// isURIRelatedTag 判断当前标签是否属于“紧邻下一个媒体 URI”的伴随标签。
// 这些标签在过滤 preloading 时需要和对应 URI 一起丢弃。
func isURIRelatedTag(line string) bool {
	for _, prefix := range []string{
		"#EXTINF",
		"#EXT-X-BYTERANGE",
		"#EXT-X-STREAM-INF",
		"#EXT-X-PROGRAM-DATE-TIME",
		"#EXT-X-DISCONTINUITY",
		"#EXT-X-CUE-OUT",
		"#EXT-X-CUE-IN",
		"#EXT-X-DATERANGE",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// looksLikePlaylist 判断目标 URL 是否仍然是 m3u8，从而继续递归走播放列表重写逻辑。
func looksLikePlaylist(target string) bool {
	target = strings.ToLower(target)
	return strings.Contains(target, ".m3u8") || strings.HasSuffix(target, ".m3u")
}
