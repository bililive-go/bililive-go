// Package flvproxy 提供 FLV 流透明代理功能
// 用于在 FFmpeg 录制时检测分段条件并主动断开连接
package flvproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	blog "github.com/bililive-go/bililive-go/src/log"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
)

var (
	// ErrSegmentRequired 需要分段的错误（触发分段条件）
	ErrSegmentRequired = errors.New("segment required: new SPS/PPS detected")
)

// 默认最小分段间隔
const DefaultMinSegmentInterval = 10 * time.Second

// FLVProxy FLV 透明代理服务器
type FLVProxy struct {
	listener    net.Listener
	port        int
	localURL    string
	upstreamURL string
	headers     map[string]string

	// 分段检测
	avcHeaderCount int
	mu             sync.Mutex

	// GOP 边缘分段支持
	pendingSegment     atomic.Bool // 待分段标志（等待下一个关键帧）
	lastSegmentAt      time.Time   // 上次分段时间
	minSegmentInterval time.Duration

	// 连接管理
	activeConn net.Conn
	connMu     sync.Mutex

	// 状态
	closed   bool
	closedMu sync.RWMutex
}

// NewFLVProxy 创建新的 FLV 代理
func NewFLVProxy(upstreamURL string, headers map[string]string) (*FLVProxy, error) {
	// 在随机端口上监听
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	addr := listener.Addr().(*net.TCPAddr)
	proxy := &FLVProxy{
		listener:           listener,
		port:               addr.Port,
		localURL:           fmt.Sprintf("http://127.0.0.1:%d/stream.flv", addr.Port),
		upstreamURL:        upstreamURL,
		headers:            headers,
		minSegmentInterval: DefaultMinSegmentInterval,
	}

	return proxy, nil
}

// RequestSegment 请求在下一个关键帧处分段
// 返回 true 表示请求已接受，false 表示距离上次分段时间过短
func (p *FLVProxy) RequestSegment() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 检查最小分段间隔
	if time.Since(p.lastSegmentAt) < p.minSegmentInterval {
		blog.GetLogger().Warnf("FLV 代理：分段请求被拒绝，距离上次分段仅 %.1f 秒（最小间隔 %.0f 秒）",
			time.Since(p.lastSegmentAt).Seconds(), p.minSegmentInterval.Seconds())
		return false
	}

	p.pendingSegment.Store(true)
	blog.GetLogger().Info("FLV 代理：已标记待分段，将在下一个关键帧处分段")
	return true
}

// IsPendingSegment 检查是否有待处理的分段请求
func (p *FLVProxy) IsPendingSegment() bool {
	return p.pendingSegment.Load()
}

// LocalURL 返回本地代理 URL（供 FFmpeg 使用）
func (p *FLVProxy) LocalURL() string {
	return p.localURL
}

// Port 返回代理端口
func (p *FLVProxy) Port() int {
	return p.port
}

// Serve 启动代理服务（阻塞）
func (p *FLVProxy) Serve(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/stream.flv", p.handleStream)

	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 流式传输，不设置写超时
	}

	bilisentry.GoWithContext(ctx, func(ctx context.Context) {
		<-ctx.Done()
		p.Close()
		server.Close()
	})

	err := server.Serve(p.listener)
	if p.isClosed() {
		return nil
	}
	return err
}

// Close 关闭代理服务
func (p *FLVProxy) Close() error {
	p.closedMu.Lock()
	p.closed = true
	p.closedMu.Unlock()

	// 关闭活动连接
	p.connMu.Lock()
	if p.activeConn != nil {
		p.activeConn.Close()
	}
	p.connMu.Unlock()

	return p.listener.Close()
}

func (p *FLVProxy) isClosed() bool {
	p.closedMu.RLock()
	defer p.closedMu.RUnlock()
	return p.closed
}

// handleStream 处理来自 FFmpeg 的流请求
func (p *FLVProxy) handleStream(w http.ResponseWriter, r *http.Request) {
	// 连接上游
	req, err := http.NewRequestWithContext(r.Context(), "GET", p.upstreamURL, nil)
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}

	req.Header.Set("User-Agent", "Chrome/59.0.3071.115")
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{
		Timeout: 0, // 流式传输，不设置超时
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to connect upstream", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 设置响应头
	w.Header().Set("Content-Type", "video/x-flv")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// 获取底层连接用于强制关闭
	hijacker, ok := w.(http.Hijacker)
	if ok {
		conn, _, err := hijacker.Hijack()
		if err == nil {
			p.connMu.Lock()
			p.activeConn = conn
			p.connMu.Unlock()
			defer func() {
				p.connMu.Lock()
				p.activeConn = nil
				p.connMu.Unlock()
			}()
		}
	}

	// 解析并转发 FLV 流
	err = p.parseAndForward(r.Context(), resp.Body, w)
	if err != nil {
		if errors.Is(err, ErrSegmentRequired) {
			blog.GetLogger().Info("FLV 代理检测到分段条件，关闭连接")
			// 强制关闭连接，触发 FFmpeg 分段
			p.forceCloseConnection()
		}
	}
}

// forceCloseConnection 强制关闭到 FFmpeg 的连接
func (p *FLVProxy) forceCloseConnection() {
	p.connMu.Lock()
	defer p.connMu.Unlock()
	if p.activeConn != nil {
		p.activeConn.Close()
		p.activeConn = nil
	}
}

// parseAndForward 解析 FLV 流并转发，同时检测分段条件
func (p *FLVProxy) parseAndForward(ctx context.Context, src io.Reader, dst io.Writer) error {
	// 重置 AVC header 计数
	p.mu.Lock()
	p.avcHeaderCount = 0
	p.mu.Unlock()

	// 读取并转发 FLV header (9 bytes)
	header := make([]byte, 9)
	if _, err := io.ReadFull(src, header); err != nil {
		return err
	}
	if _, err := dst.Write(header); err != nil {
		return err
	}

	// 循环处理 tag
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 读取 PreviousTagSize (4 bytes) + Tag Header (11 bytes)
		tagHeader := make([]byte, 15)
		if _, err := io.ReadFull(src, tagHeader); err != nil {
			return err
		}

		tagType := tagHeader[4]
		dataSize := uint32(tagHeader[5])<<16 | uint32(tagHeader[6])<<8 | uint32(tagHeader[7])

		// 读取 tag data
		tagData := make([]byte, dataSize)
		if _, err := io.ReadFull(src, tagData); err != nil {
			return err
		}

		// 检查视频 tag 中的分段条件
		if tagType == 9 && len(tagData) > 0 { // video tag
			if err := p.checkVideoTag(tagData); err != nil {
				// 先转发当前 tag，然后返回分段错误
				dst.Write(tagHeader)
				dst.Write(tagData)
				return err
			}
		}

		// 转发 tag
		if _, err := dst.Write(tagHeader); err != nil {
			return err
		}
		if _, err := dst.Write(tagData); err != nil {
			return err
		}
	}
}

// checkVideoTag 检查视频 tag 是否包含分段触发条件
// 实现 GOP 边缘分段：在关键帧处才真正触发分段
func (p *FLVProxy) checkVideoTag(data []byte) error {
	if len(data) < 2 {
		return nil
	}

	// 解析 video tag header
	frameType := (data[0] >> 4) & 0x0F
	codecID := data[0] & 0x0F

	// 只处理 AVC (H.264)
	if codecID != 7 { // AVC
		return nil
	}

	avcPacketType := data[1]

	// AVCSeqHeader = 0, AVCNALU = 1, AVCEndSeq = 2
	if avcPacketType == 0 { // AVC Sequence Header (SPS/PPS)
		p.mu.Lock()
		p.avcHeaderCount++
		count := p.avcHeaderCount
		p.mu.Unlock()

		if count > 1 {
			// 检测到新的 SPS/PPS，标记待分段
			blog.GetLogger().Infof("FLV 代理检测到第 %d 个 AVC Sequence Header，标记待分段", count)
			p.pendingSegment.Store(true)
		}
	}

	// 检查是否为关键帧（I-frame）
	// frameType: 1 = keyframe, 2 = inter frame, 3 = disposable inter frame
	isKeyframe := frameType == 1 && avcPacketType == 1 // keyframe + NALU data

	// 如果有待分段标志且当前是关键帧，触发分段
	if isKeyframe && p.pendingSegment.Load() {
		p.pendingSegment.Store(false)

		// 更新上次分段时间
		p.mu.Lock()
		p.lastSegmentAt = time.Now()
		p.mu.Unlock()

		blog.GetLogger().Info("FLV 代理：在关键帧处触发分段")
		return ErrSegmentRequired
	}

	return nil
}
