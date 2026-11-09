package recorders

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluele/gcache"
	"github.com/sirupsen/logrus"
	gomock "go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	livemock "github.com/bililive-go/bililive-go/src/live/mock"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/pkg/parser"
	"github.com/bililive-go/bililive-go/src/pkg/parser/ffmpeg"
	"github.com/bililive-go/bililive-go/src/pkg/parser/native/flv"
	"github.com/bililive-go/bililive-go/src/pkg/proxy"
	"github.com/bililive-go/bililive-go/src/types"
)

// recordingFLVFixture 生成两个含静音 PCM 样本的 FLV 音频标签。
// 这里只验证原生录制链路与标签内容，不把文件存在等同于媒体可播放。
func recordingFLVFixture() []byte {
	data := []byte{'F', 'L', 'V', 1, 4, 0, 0, 0, 9, 0, 0, 0, 0}
	for _, timestamp := range []byte{0, 20} {
		payload := append([]byte{0x30}, bytes.Repeat([]byte{0x80}, 16)...)
		header := []byte{8, 0, 0, byte(len(payload)), 0, 0, timestamp, 0, 0, 0, 0}
		data = append(data, header...)
		data = append(data, payload...)
		data = binary.BigEndian.AppendUint32(data, uint32(len(header)+len(payload)))
	}
	return data
}

// TestTryRecordNativeHTTPOutput 使用真实 HTTP、探测代理、下载器选择和原生 parser。
// 仅平台元信息使用 mock，不替换 newParser，也不依赖 FFmpeg 或外部直播站点。
// 原生 parser 把有限流 EOF 作为断流错误上报；完整标签已落盘不代表正常停止或重连成功。
func TestTryRecordNativeHTTPOutput(t *testing.T) {
	isolateRecordingTestProxyEnv(t)
	fixture := recordingFLVFixture()
	// 最后的 PreviousTagSize 只有读取下一标签时才会被原生 parser 写出。
	completeOutput := fixture[:len(fixture)-4]
	tests := []struct {
		name      string
		input     []byte
		want      []byte
		wantError string
	}{
		{
			name:      "完整音频标签经真实链路落盘",
			input:     fixture,
			want:      completeOutput,
			wantError: "EOF",
		},
		{
			name:  "探测阶段音频标签截断保留第一标签并报告错误",
			input: fixture[:len(fixture)-12],
			// 探测代理转发第二标签头，但丢弃截断的标签体；原生 parser 读音频头遇到 EOF，
			// 尚未写出该标签头，输出止于第一标签体末尾。这不保证探测后的标签原子落盘。
			want:      fixture[:41],
			wantError: "EOF",
		},
		{
			name:      "非法FLV签名不能产生有效输出",
			input:     []byte("not a valid FLV stream"),
			want:      []byte{},
			wantError: "not flv stream",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 全局配置不可并行修改。
			restore := swapTestConfig(t)
			defer restore()
			cfg := configs.GetCurrentConfig()
			cfg.OutPutPath = t.TempDir()
			cfg.OutputTmpl = "recording.flv"
			cfg.Feature.DownloaderType = configs.DownloaderNative

			var requests atomic.Int32
			var headersOK atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests.Add(1)
				if req.URL.Path != "/stream.flv" {
					http.NotFound(w, req)
					return
				}
				headersOK.Store(req.Header.Get("X-Recording-Test") == "local-only")
				w.Header().Set("Content-Type", "video/x-flv")
				_, _ = w.Write(tt.input)
			}))
			defer server.Close()
			streamURL, err := url.Parse(server.URL + "/stream.flv")
			if err != nil {
				t.Fatal(err)
			}
			ctrl := gomock.NewController(t)
			room := livemock.NewMockLive(ctrl)
			logger := livelogger.New(0, logrus.Fields{"test": t.Name()})
			room.EXPECT().GetRawUrl().Return(server.URL + "/room").AnyTimes()
			room.EXPECT().GetLogger().Return(logger).AnyTimes()
			room.EXPECT().GetPlatformCNName().Return("本地测试").AnyTimes()
			room.EXPECT().GetOptions().Return(live.MustNewOptions()).AnyTimes()
			room.EXPECT().GetStreamInfos().Return([]*live.StreamUrlInfo{{
				Url:                  streamURL,
				Format:               "flv",
				HeadersForDownloader: map[string]string{"X-Recording-Test": "local-only"},
			}}, nil)
			inst := &instance.Instance{Cache: gcache.New(1).LRU().Build()}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = context.WithValue(ctx, instance.Key, inst)
			events.NewDispatcher(ctx)
			if err := inst.Cache.Set(room, &live.Info{Live: room, RoomName: "本地流"}); err != nil {
				t.Fatal(err)
			}
			rec, err := NewRecorder(ctx, room)
			if err != nil {
				t.Fatal(err)
			}
			r := rec.(*recorder)
			defer stopRecordingTestParser(t, r)
			r.tryRecord(ctx)
			if ctx.Err() != nil {
				t.Fatalf("本地有限流未及时结束: %v", ctx.Err())
			}
			if _, ok := r.parser.(*flv.Parser); !ok {
				t.Fatalf("必须实际使用原生 FLV parser，得到 %T", r.parser)
			}
			if requests.Load() == 0 || !headersOK.Load() {
				t.Fatalf("未带预期请求头读取本地流: 请求数=%d，请求头正确=%v", requests.Load(), headersOK.Load())
			}
			output, err := os.ReadFile(filepath.Join(cfg.OutPutPath, "recording.flv"))
			if err != nil {
				t.Fatalf("读取真实录制输出失败: %v", err)
			}
			if !bytes.Equal(output, tt.want) {
				t.Fatalf("落盘内容不符合预期: 得到 %x，期望 %x", output, tt.want)
			}
			logs := logger.GetLogs()
			if !strings.Contains(logs, "failed to parse live stream") || !strings.Contains(logs, tt.wantError) {
				t.Fatalf("断流或非法输入应报告实际 parser 错误 %q，日志: %s", tt.wantError, logs)
			}
			if tt.name != tests[0].name && bytes.Equal(output, completeOutput) {
				t.Fatal("截断或非法流不应被当成完整输出")
			}
		})
	}
}

// TestTryRecordFFmpegMediaOutput 验证真实 recorder 与 FFmpeg parser 的媒体产物。
// 工具缺失时明确跳过；CI 可用 BGO_REQUIRE_MEDIA_TESTS=1 禁止静默跳过。
func TestTryRecordFFmpegMediaOutput(t *testing.T) {
	isolateRecordingTestProxyEnv(t)
	ffmpegPath, ffmpegErr := exec.LookPath("ffmpeg")
	ffprobePath, ffprobeErr := exec.LookPath("ffprobe")
	if ffmpegErr != nil || ffprobeErr != nil {
		if os.Getenv("BGO_REQUIRE_MEDIA_TESTS") == "1" {
			t.Fatalf("媒体测试必需工具不可用: ffmpeg=%v ffprobe=%v", ffmpegErr, ffprobeErr)
		}
		t.Skipf("真实媒体测试需要 ffmpeg 和 ffprobe: %v; %v", ffmpegErr, ffprobeErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runTool := func(name string, args ...string) []byte {
		t.Helper()
		output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("媒体工具执行失败: %v\n%s", err, output)
		}
		return output
	}
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "source.flv")
	runTool(ffmpegPath, "-nostdin", "-v", "error", "-f", "lavfi", "-i",
		"testsrc2=size=160x120:rate=10", "-t", "2", "-c:v", "libx264",
		"-pix_fmt", "yuv420p", "-f", "flv", source)

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/stream.flv" || req.Header.Get("X-Recording-Test") != "media-local" {
			http.Error(w, "请求路径或请求头错误", http.StatusBadRequest)
			return
		}
		requests.Add(1)
		http.ServeFile(w, req, source)
	}))
	defer server.Close()
	streamURL, err := url.Parse(server.URL + "/stream.flv")
	if err != nil {
		t.Fatal(err)
	}
	restore := swapTestConfig(t)
	defer restore()
	cfg := configs.GetCurrentConfig()
	cfg.OutPutPath = t.TempDir()
	cfg.OutputTmpl = "recording.flv"
	cfg.FfmpegPath = ffmpegPath
	cfg.Feature.DownloaderType = configs.DownloaderFFmpeg
	cfg.Feature.EnableFlvProxySegment = false
	cfg.OnRecordFinished.FixFlvAtFirst = false
	cfg.OnRecordFinished.ConvertToMp4 = false

	ctrl := gomock.NewController(t)
	room := livemock.NewMockLive(ctrl)
	logger := livelogger.New(0, logrus.Fields{"test": t.Name()})
	room.EXPECT().GetRawUrl().Return(server.URL + "/room").AnyTimes()
	room.EXPECT().GetLogger().Return(logger).AnyTimes()
	room.EXPECT().GetPlatformCNName().Return("本地测试").AnyTimes()
	room.EXPECT().GetOptions().Return(live.MustNewOptions()).AnyTimes()
	room.EXPECT().GetLiveId().Return(types.LiveID("local-media")).AnyTimes()
	room.EXPECT().GetStreamInfos().Return([]*live.StreamUrlInfo{{
		Url: streamURL, Format: "flv",
		HeadersForDownloader: map[string]string{"X-Recording-Test": "media-local"},
	}}, nil)
	inst := &instance.Instance{Cache: gcache.New(1).LRU().Build()}
	ctx = context.WithValue(ctx, instance.Key, inst)
	events.NewDispatcher(ctx)
	if err := inst.Cache.Set(room, &live.Info{Live: room, RoomName: "本地媒体"}); err != nil {
		t.Fatal(err)
	}
	rec, err := NewRecorder(ctx, room)
	if err != nil {
		t.Fatal(err)
	}
	r := rec.(*recorder)
	defer stopRecordingTestParser(t, r)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.tryRecord(ctx)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		// FFmpeg parser 的 Wait 不监听 context，必须显式 Stop。
		// 先关闭 stop，防止超时时 parser 还没安装导致漏停。
		close(r.stop)
		stopRecordingTestParser(t, r)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("停止 FFmpeg 后录制仍未退出")
		}
		t.Fatalf("真实媒体录制超时: %v", ctx.Err())
	}
	if _, ok := r.parser.(*ffmpeg.Parser); !ok {
		t.Fatalf("未使用真实 FFmpeg parser: %T", r.parser)
	}
	if requests.Load() == 0 {
		t.Fatal("真实录制没有请求本地媒体")
	}
	if strings.Contains(logger.GetLogs(), "failed to parse live stream") {
		t.Fatalf("真实 FFmpeg 录制失败: %s", logger.GetLogs())
	}
	output := filepath.Join(cfg.OutPutPath, "recording.flv")
	stat, err := os.Stat(output)
	if err != nil || stat.Size() == 0 {
		t.Fatalf("未产生非空录制文件: %v", err)
	}
	probe := runTool(ffprobePath, "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", output)
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(probe)), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 1.8 || seconds > 3 {
		t.Fatalf("录制时长不符合两秒源: %q, %v", probe, err)
	}
	runTool(ffmpegPath, "-nostdin", "-v", "error", "-xerror", "-i", output,
		"-map", "0:v:0", "-abort_on", "empty_output", "-f", "null", "-")
}

// stopRecordingTestParser 仅停止下载器，不替换录制协程仍可能读取的 parser 引用。
func stopRecordingTestParser(t *testing.T, r *recorder) {
	t.Helper()
	if p := r.getParser(); p != nil {
		if err := p.Stop(); err != nil {
			t.Errorf("停止测试 parser 失败: %v", err)
		}
	}
}

func TestStopRecordingTestParserKeepsInstalledReference(t *testing.T) {
	p, err := parser.New(ffmpeg.Name, nil, livelogger.New(0, logrus.Fields{"test": t.Name()}))
	if err != nil {
		t.Fatal(err)
	}
	r := &recorder{parser: p, parserLock: new(sync.RWMutex), stop: make(chan struct{})}
	// 模拟 tryRecord 已安装 parser，但还没有读取 r.parser 调用 ParseLiveStream。
	close(r.stop)
	stopRecordingTestParser(t, r)
	if r.getParser() != p {
		t.Fatal("超时清理清空了录制协程仍需读取的 parser")
	}
	// 使用真实 FFmpeg parser：停止后应在查找工具和访问流参数前返回，不启动子进程。
	if err := r.parser.ParseLiveStream(context.Background(), nil, nil, ""); err == nil || !strings.Contains(err.Error(), "parser stopped") {
		t.Fatalf("停止后不应继续启动录制: %v", err)
	}
	// 超时分支之后的 defer 也不能替换 parser 引用。
	stopRecordingTestParser(t, r)
	if r.getParser() != p {
		t.Fatal("defer 清理替换了 parser 引用")
	}
}

// isolateRecordingTestProxyEnv 让本地合成流只走回环连接；t.Setenv 会在测试结束后还原环境。
func isolateRecordingTestProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, "")
	}
}

func TestRecordingProxyEnvironmentIsolation(t *testing.T) {
	restore := swapTestConfig(t)
	defer restore()
	keys := []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"}
	const proxyURL = "http://127.0.0.1:9"
	for _, key := range keys {
		t.Setenv(key, proxyURL)
	}
	t.Run("清空所有代理变量", func(t *testing.T) {
		isolateRecordingTestProxyEnv(t)
		for _, key := range keys {
			if got := os.Getenv(key); got != "" {
				t.Errorf("%s 未隔离: %q", key, got)
			}
		}
		if got := proxy.GetDownloadProxyURL(); got != "" {
			t.Fatalf("下载器仍继承代理: %q", got)
		}
	})
	for _, key := range keys {
		if got := os.Getenv(key); got != proxyURL {
			t.Errorf("子测试结束后 %s 未还原: %q", key, got)
		}
	}
}
