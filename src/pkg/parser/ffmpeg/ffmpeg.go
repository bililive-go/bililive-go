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
	"sort"
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
	"github.com/bililive-go/bililive-go/src/tools"
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
		stopped:     make(chan struct{}),
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
	stopped     chan struct{} // Stop() 被调用时关闭，用于中断 FFmpeg 就绪等待
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
	// 非阻塞发送状态请求：如果 scheduler 已退出或 buffer 已满，直接返回 nil
	select {
	case p.statusReq <- struct{}{}:
	default:
		return nil, nil
	}
	// 等待响应，带超时保护：如果 scheduler 已退出（statusResp 被关闭），
	// 读取会立即返回零值；如果 scheduler 卡住，3 秒后超时返回
	select {
	case resp, ok := <-p.statusResp:
		if !ok {
			return nil, nil
		}
		return resp, nil
	case <-time.After(3 * time.Second):
		return nil, nil
	}
}

// ParseLiveStream 启动 FFmpeg 进程录制直播流。
//
// ⚠️ 已知问题（负负得正）：
// 本函数的 ctx 参数实际上无法取消函数执行——核心阻塞点 cmd.Wait()（第 272 行）不监听 ctx.Done()，
// FFmpeg 进程的终止完全依赖外部调用 Stop() 方法。
//
// 然而这个"bug"意外地保护了录制行为：调用链中的 ctx 可能源自 HTTP handler 的 request context
// （handler → AddRecorder → recorder.Start → run → tryRecord → ParseLiveStream），
// 若 ParseLiveStream 正确响应 ctx 取消，录制会在 HTTP 请求结束后被意外终止。
//
// 正确修复需要同时解决整条 context 传播链路（将 request context 替换为应用级 context），
// 影响范围广，暂不在此处理。当前录制的停止完全由 recorder.Close() → parser.Stop() 控制。
func (p *Parser) ParseLiveStream(ctx context.Context, streamUrlInfo *live.StreamUrlInfo, live live.Live, file string) (err error) {
	// Stop() 已被调用（p.closeOnce 已执行）时直接返回：此后再启动 FFmpeg 进程将无法
	// 通过 Stop() 终止，会造成进程泄漏。这里先做一次廉价的非阻塞检查快速失败；真正消除
	// 竞态的检查在下方持有 p.cmdLock 后、进程 Start() 前再做一次（与 Stop() 互斥）。
	select {
	case <-p.stopped:
		return fmt.Errorf("parser stopped")
	default:
	}
	url := streamUrlInfo.Url
	ffmpegPath, err := utils.GetFFmpegPathForLive(ctx, live)
	if err != nil {
		// 找不到 FFmpeg 且后台异步初始化（tools.FFmpegAsyncInit）仍在检测/下载时，
		// 挂起等待其进入终态后重查一次，避免开播瞬间陷入"找不到 FFmpeg → 5 秒重试"
		// 的失败循环；下载一完成录制立即开始。等待可被 ctx 取消或 Stop() 中断。
		if waitErr := tools.WaitFFmpegAsyncInitDone(ctx, p.stopped); waitErr != nil {
			return waitErr
		}
		if ffmpegPath, err = utils.GetFFmpegPathForLive(ctx, live); err != nil {
			return err
		}
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

	args := buildInputArgs(inputArgs{
		timeoutInUs: p.timeoutInUs,
		inputURL:    inputURL,
		headers:     headers,
		userAgent:   ffUserAgent,
		referer:     referer,
		useProxy:    useProxy,
		rateLimit:   url.Hostname() != "localhost" && !useProxy,
	})

	// 只录音频模式：添加 -vn 参数忽略视频流
	if p.audioOnly {
		args = append(args, "-vn")
		p.logger.Info("只录音频模式已启用，将忽略视频流")
	}

	args = append(args, "-c", "copy")

	args = append(args, p.videoSplitArgs(live)...)

	args = append(args, file)

	// p.cmd operations need p.cmdLock
	func() {
		p.cmdLock.Lock()
		defer p.cmdLock.Unlock()
		// 与 Stop() 互斥的权威检查：Stop() 会先 close(p.stopped) 再抢 p.cmdLock 处理 p.cmd。
		// 若此处观察到已 stopped，说明 Stop() 的清理不会覆盖到本次即将新建的进程，必须放弃
		// 启动，否则该进程无人回收。错误由外层 if err != nil 分支统一清理 FLV 代理并返回。
		select {
		case <-p.stopped:
			err = fmt.Errorf("parser stopped")
			return
		default:
		}
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
	// 注意：cmd.Wait() 不监听 ctx.Done()，见函数顶部注释。
	// 停止 FFmpeg 的唯一途径是通过 Stop() 方法。
	err = p.cmd.Wait()

	// 停止 FLV 代理
	p.stopFlvProxy()

	if err != nil {
		return err
	}
	return nil
}

// inputArgs 是 ParseLiveStream 组装"输入侧"参数（-i 及其之前的所有选项）所需的输入
type inputArgs struct {
	timeoutInUs string
	inputURL    string
	headers     map[string]string
	userAgent   string
	referer     string
	useProxy    bool
	rateLimit   bool
}

// buildInputArgs 组装 FFmpeg 的输入侧参数。
//
// 关键约束：所有请求头相关选项必须出现在 -i 之前。FFmpeg 中位于输入文件之后的选项属于
// "输出文件"，放在 -i 之后会被静默丢弃（实测：服务端只收到 -user_agent/-referer，收不到 Cookie），
// 这会让斗鱼登录 cookie 等"带登录态取流"的能力形同不存在。
func buildInputArgs(a inputArgs) []string {
	args := []string{
		"-nostats",
		"-progress", "-",
		"-y",
	}

	// 为了测试方便，本地地址不需要限速
	// 使用代理时，FFmpeg 连接的是本地地址，不需要限速
	if a.rateLimit {
		args = append(args, "-re")
	}

	// 使用代理时，不需要设置 User-Agent 和 Referer（代理会处理）
	if a.useProxy {
		return append(args,
			"-rw_timeout", a.timeoutInUs,
			"-i", a.inputURL,
		)
	}
	args = append(args,
		"-user_agent", a.userAgent,
		"-referer", a.referer,
	)
	if extraHeaders := buildExtraHeaders(a.headers); extraHeaders != "" {
		args = append(args, "-headers", extraHeaders)
	}
	return append(args,
		"-rw_timeout", a.timeoutInUs,
		"-i", a.inputURL,
	)
}

// buildExtraHeaders 把除 User-Agent/Referer 外的请求头合并成 ffmpeg `-headers` 需要的单个字符串
// （各头之间用 \r\n 分隔，与浏览器/HTTP 报文一致）。
// 必须合并而不是逐个传 `-headers`：ffmpeg 的 -headers 是"整体赋值"型选项，传两次就只剩最后一个，
// 实测同时传 Cookie 与自定义头时 Cookie 会被覆盖掉。按 key 排序保证参数序列稳定、可测。
func buildExtraHeaders(headers map[string]string) string {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		if k == "User-Agent" || k == "Referer" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if headers[k] == "" {
			continue
		}
		parts = append(parts, k+": "+headers[k])
	}
	return strings.Join(parts, "\r\n")
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
		// 中断可能正在进行的 FFmpeg 就绪等待
		close(p.stopped)

		// 先停止 FLV 代理
		p.stopFlvProxy()

		p.cmdLock.Lock()
		defer p.cmdLock.Unlock()
		if p.cmd != nil && p.cmd.ProcessState == nil {
			if p.cmdStdIn != nil && p.cmd.Process != nil {
				if _, err = p.cmdStdIn.Write([]byte("q")); err != nil {
					err = fmt.Errorf("error sending stop command to ffmpeg: %v", err)
				}
				// 启动强制退出 goroutine：如果 FFmpeg 3 秒内未响应 "q" 命令退出
				// （例如正在等待 HLS m3u8 网络超时），则强制杀掉进程
				process := p.cmd.Process
				go func() {
					time.Sleep(3 * time.Second)
					// 直接尝试 Kill，不读 ProcessState（会与 Wait() 数据竞争）
					// 如果进程已退出，Kill 会返回 os.ErrProcessDone 之类的错误，安全忽略即可
					if killErr := process.Kill(); killErr != nil {
						// 进程已正常退出，无需额外处理
						_ = killErr
					}
				}()
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

func (p *Parser) videoSplitArgs(room live.Live) []string {
	cfg := configs.GetCurrentConfig()
	var maxFileSize int64
	if cfg != nil {
		maxFileSize = cfg.GetEffectiveConfigForRoom(room.GetRawUrl()).VideoSplitStrategies.MaxFileSize.Bytes()
	}
	if maxFileSize < 0 {
		p.logger.Infof("Invalid MaxFileSize: %d", maxFileSize)
	} else if maxFileSize > 0 {
		return []string{"-fs", strconv.FormatInt(maxFileSize, 10)}
	}

	return nil
}
