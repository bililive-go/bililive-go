package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/flvproxy"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/pkg/parser"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	Name      = "ffmpeg"
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/59.0.3071.115 Safari/537.36"
)

func init() {
	parser.Register(Name, new(builder))
}

type builder struct{}

func (b *builder) Build(cfg map[string]string, logger *livelogger.LiveLogger) (parser.Parser, error) {
	audioOnly := cfg["audio_only"] == "true"
	useFlvProxy := cfg["use_flv_proxy"] == "true"
	return &Parser{
		closeOnce:   new(sync.Once),
		statusReq:   make(chan struct{}, 1),
		statusResp:  make(chan map[string]interface{}, 1),
		timeoutInUs: cfg["timeout_in_us"],
		audioOnly:   audioOnly,
		useFlvProxy: useFlvProxy,
		logger:      logger,
	}, nil
}

type Parser struct {
	cmd         *exec.Cmd
	cmdStdIn    io.WriteCloser
	cmdStdout   io.ReadCloser
	closeOnce   *sync.Once
	timeoutInUs string
	audioOnly   bool
	useFlvProxy bool // 是否使用 FLV 代理分段

	statusReq  chan struct{}
	statusResp chan map[string]interface{}
	cmdLock    sync.Mutex
	logger     *livelogger.LiveLogger

	// FLV 代理相关
	flvProxy     *flvproxy.FLVProxy
	flvProxyMu   sync.Mutex
	flvProxyCtx  context.Context
	flvProxyStop context.CancelFunc
}

func (p *Parser) scanFFmpegStatus() <-chan []byte {
	ch := make(chan []byte)
	br := bufio.NewScanner(p.cmdStdout)
	br.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}

		if idx := bytes.Index(data, []byte("progress=continue\n")); idx >= 0 {
			return idx + 1, data[0:idx], nil
		}

		return 0, nil, nil
	})
	bilisentry.Go(func() {
		defer close(ch)
		for br.Scan() {
			ch <- br.Bytes()
		}
	})
	return ch
}

func (p *Parser) decodeFFmpegStatus(b []byte) (status map[string]interface{}) {
	status = map[string]interface{}{
		"parser": Name,
	}
	s := bufio.NewScanner(bytes.NewReader(b))
	s.Split(bufio.ScanLines)
	for s.Scan() {
		split := bytes.SplitN(s.Bytes(), []byte("="), 2)
		if len(split) != 2 {
			continue
		}
		status[string(bytes.TrimSpace(split[0]))] = string(bytes.TrimSpace(split[1]))
	}
	return
}

func (p *Parser) scheduler() {
	defer close(p.statusResp)
	statusCh := p.scanFFmpegStatus()
	for {
		select {
		case <-p.statusReq:
			select {
			case b, ok := <-statusCh:
				if !ok {
					return
				}
				p.statusResp <- p.decodeFFmpegStatus(b)
			case <-time.After(time.Second * 3):
				p.statusResp <- nil
			}
		default:
			if _, ok := <-statusCh; !ok {
				return
			}
		}
	}
}

func (p *Parser) Status() (map[string]interface{}, error) {
	// TODO: check parser is running
	p.statusReq <- struct{}{}
	return <-p.statusResp, nil
}

func (p *Parser) ParseLiveStream(ctx context.Context, streamUrlInfo *live.StreamUrlInfo, live live.Live, file string) (err error) {
	url := streamUrlInfo.Url
	ffmpegPath, err := utils.GetFFmpegPathForLive(ctx, live)
	if err != nil {
		return err
	}
	headers := streamUrlInfo.HeadersForDownloader
	ffUserAgent, exists := headers["User-Agent"]
	if !exists {
		ffUserAgent = userAgent
	}
	referer, exists := headers["Referer"]
	if !exists {
		referer = live.GetRawUrl()
	}

	// 判断是否使用 FLV 代理
	inputURL := url.String()
	useProxy := p.useFlvProxy && p.isFlvStream(url)

	if useProxy {
		// 启动 FLV 代理
		proxy, proxyErr := flvproxy.NewFLVProxy(url.String(), headers)
		if proxyErr != nil {
			p.logger.Warnf("无法创建 FLV 代理，将直接连接上游: %v", proxyErr)
			useProxy = false
		} else {
			p.flvProxyMu.Lock()
			p.flvProxy = proxy
			p.flvProxyCtx, p.flvProxyStop = context.WithCancel(ctx)
			p.flvProxyMu.Unlock()

			// 在后台启动代理服务
			bilisentry.GoWithContext(p.flvProxyCtx, func(ctx context.Context) {
				if err := proxy.Serve(ctx); err != nil {
					p.logger.Debugf("FLV 代理服务退出: %v", err)
				}
			})

			// 使用代理 URL
			inputURL = proxy.LocalURL()
			p.logger.Infof("FLV 代理已启动，端口 %d，检测 SPS/PPS 变化自动分段", proxy.Port())
		}
	}

	args := []string{
		"-nostats",
		"-progress", "-",
		"-y",
	}

	// 为了测试方便，本地地址不需要限速
	// 使用代理时，FFmpeg 连接的是本地地址，不需要限速
	if url.Hostname() != "localhost" && !useProxy {
		args = append(args, "-re")
	}

	// 使用代理时，不需要设置 User-Agent 和 Referer（代理会处理）
	if useProxy {
		args = append(args,
			"-rw_timeout", p.timeoutInUs,
			"-i", inputURL,
		)
	} else {
		args = append(args,
			"-user_agent", ffUserAgent,
			"-referer", referer,
			"-rw_timeout", p.timeoutInUs,
			"-i", inputURL,
		)
	}

	// 只录音频模式：添加 -vn 参数忽略视频流
	if p.audioOnly {
		args = append(args, "-vn")
		p.logger.Info("只录音频模式已启用，将忽略视频流")
	}

	args = append(args, "-c", "copy")

	// 不使用代理时，添加额外的请求头
	if !useProxy {
		for k, v := range headers {
			if k == "User-Agent" || k == "Referer" {
				continue
			}
			args = append(args, "-headers", k+": "+v)
		}
	}

	cfg := configs.GetCurrentConfig()
	MaxFileSize := 0
	if cfg != nil {
		MaxFileSize = cfg.VideoSplitStrategies.MaxFileSize
	}
	if MaxFileSize < 0 {
		p.logger.Infof("Invalid MaxFileSize: %d", MaxFileSize)
	} else if MaxFileSize > 0 {
		args = append(args, "-fs", strconv.Itoa(MaxFileSize))
	}

	args = append(args, file)

	// p.cmd operations need p.cmdLock
	func() {
		p.cmdLock.Lock()
		defer p.cmdLock.Unlock()
		p.cmd = exec.Command(ffmpegPath, args...)
		if p.cmdStdIn, err = p.cmd.StdinPipe(); err != nil {
			return
		}
		if p.cmdStdout, err = p.cmd.StdoutPipe(); err != nil {
			return
		}
		// 将 ffmpeg 的 stderr 输出写入到 live logger，同时也输出到 os.Stderr
		p.cmd.Stderr = io.MultiWriter(
			utils.NewLogFilterWriter(os.Stderr),
			utils.NewLoggerWriter(p.logger),
		)
		if err = p.cmd.Start(); err != nil {
			if p.cmd.Process != nil {
				p.cmd.Process.Kill()
			}
			return
		}
	}()
	if err != nil {
		p.stopFlvProxy()
		return err
	}

	bilisentry.Go(p.scheduler)
	err = p.cmd.Wait()

	// 停止 FLV 代理
	p.stopFlvProxy()

	if err != nil {
		return err
	}
	return nil
}

// isFlvStream 判断 URL 是否指向 FLV 流
func (p *Parser) isFlvStream(u *url.URL) bool {
	path := strings.ToLower(u.Path)
	// 检查路径后缀
	if strings.HasSuffix(path, ".flv") {
		return true
	}
	// 检查查询参数中是否有 format=flv
	query := strings.ToLower(u.RawQuery)
	return strings.Contains(query, "format=flv")
}

// stopFlvProxy 停止 FLV 代理
func (p *Parser) stopFlvProxy() {
	p.flvProxyMu.Lock()
	defer p.flvProxyMu.Unlock()
	if p.flvProxyStop != nil {
		p.flvProxyStop()
		p.flvProxyStop = nil
	}
	if p.flvProxy != nil {
		p.flvProxy.Close()
		p.flvProxy = nil
	}
}

func (p *Parser) Stop() (err error) {
	p.closeOnce.Do(func() {
		// 先停止 FLV 代理
		p.stopFlvProxy()

		p.cmdLock.Lock()
		defer p.cmdLock.Unlock()
		if p.cmd != nil && p.cmd.ProcessState == nil {
			if p.cmdStdIn != nil && p.cmd.Process != nil {
				if _, err = p.cmdStdIn.Write([]byte("q")); err != nil {
					err = fmt.Errorf("error sending stop command to ffmpeg: %v", err)
				}
			} else if p.cmdStdIn == nil {
				err = fmt.Errorf("p.cmdStdIn == nil")
			} else if p.cmd.Process == nil {
				err = fmt.Errorf("p.cmd.Process == nil")
			}
		}
	})
	return err
}

// GetPID 返回 ffmpeg 进程的 PID
// 如果进程未启动或已退出，返回 0
func (p *Parser) GetPID() int {
	p.cmdLock.Lock()
	defer p.cmdLock.Unlock()
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

// RequestSegment 请求在下一个关键帧处分段
// 此方法仅在使用 FLV 代理时有效
// 返回 true 表示请求已接受，false 表示未使用 FLV 代理或请求被拒绝
func (p *Parser) RequestSegment() bool {
	p.flvProxyMu.Lock()
	defer p.flvProxyMu.Unlock()

	if p.flvProxy == nil {
		p.logger.Warn("无法请求分段：FLV 代理未启用")
		return false
	}

	return p.flvProxy.RequestSegment()
}

// HasFlvProxy 检查当前是否使用 FLV 代理
func (p *Parser) HasFlvProxy() bool {
	p.flvProxyMu.Lock()
	defer p.flvProxyMu.Unlock()
	return p.flvProxy != nil
}
