//go:build dev

package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestFFmpegMediaSmoke 使用真实 FFmpeg/ffprobe 和本机合成流；不是 bgo 端到端测试。
func TestFFmpegMediaSmoke(t *testing.T) {
	ffmpeg, ffmpegErr := exec.LookPath("ffmpeg")
	ffprobe, ffprobeErr := exec.LookPath("ffprobe")
	if ffmpegErr != nil || ffprobeErr != nil {
		if os.Getenv("BGO_REQUIRE_MEDIA_TESTS") == "1" {
			t.Fatalf("必须安装媒体测试依赖: ffmpeg=%v ffprobe=%v", ffmpegErr, ffprobeErr)
		}
		t.Skipf("媒体测试未运行，缺少 FFmpeg/ffprobe: %v / %v", ffmpegErr, ffprobeErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if output, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("生成合成媒体失败: %v\n%s", err, output)
		}
	}
	// 两秒、160×90 的合成画面和音频，输入不依赖外部平台或真实直播间。
	flv := filepath.Join(fixture, "source.flv")
	run("-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "2",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "10",
		"-c:a", "aac", "-f", "flv", flv)
	run("-nostdin", "-v", "error", "-i", flv, "-c", "copy", "-hls_time", "1", "-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(fixture, "part%d.ts"), filepath.Join(fixture, "source.m3u8"))
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/live/basic.flv", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, flv) })
	mux.Handle("/hls/", http.StripPrefix("/hls/", http.FileServer(http.Dir(fixture))))
	server := httptest.NewServer(mux)
	defer server.Close()

	for _, format := range []string{"flv", "hls"} {
		t.Run(format, func(t *testing.T) {
			scenario := validScenario()
			scenario.Stream.Format = format
			scenario.Stream.Duration = 2 * time.Second
			if format == "hls" {
				scenario.Name = "source"
			}
			scenario.Expected.MinDuration = 1800 * time.Millisecond
			runner := NewTestRunner(server.URL, t.TempDir())
			runner.Verbose = false
			runner.FFmpegPath, runner.FFprobePath = ffmpeg, ffprobe
			result, err := runner.RunScenario(ctx, scenario)
			if err != nil || !result.Success || !result.OutputPlayable || result.OutputFileSize == 0 {
				t.Fatalf("真实媒体冒烟失败: result=%+v err=%v", result, err)
			}
			if result.OutputDuration > 3*time.Second {
				t.Fatalf("意外时长: %s", result.OutputDuration)
			}
			t.Logf("真实 %s 输出: %d 字节，%s，ffprobe 和完整解码通过", format, result.OutputFileSize, result.OutputDuration)
			// 同一真实产物不能满足虚假的更长时长要求。
			if err := runner.validateOutput(ctx, result.OutputPath, Expected{OutputPlayable: true, MinDuration: 4 * time.Second}, &TestResult{}); err == nil {
				t.Fatal("过短媒体被误报通过")
			}
			invalid := filepath.Join(t.TempDir(), "invalid.flv")
			if err := os.WriteFile(invalid, []byte("not media"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := runner.validateOutput(ctx, invalid, scenario.Expected, &TestResult{}); err == nil {
				t.Fatal("损坏文件被误报通过")
			}
		})
	}
}
