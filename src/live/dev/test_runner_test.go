//go:build dev

package dev

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateOutputRejectsMissingFFprobe(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	path := filepath.Join(t.TempDir(), "invalid.flv")
	if err := os.WriteFile(path, []byte("不是媒体文件"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner("", t.TempDir())
	runner.Verbose = false
	result := &TestResult{}
	if err := runner.validateOutput(context.Background(), path, Expected{OutputPlayable: true}, result); err == nil {
		t.Fatal("缺少 ffprobe 时必须验证失败，不能把任意非空文件标记为可播放")
	}
	if result.OutputPlayable {
		t.Fatal("未验证的文件被标记为可播放")
	}
}

func TestBuildStreamURLUsesHLSEndpoint(t *testing.T) {
	runner := NewTestRunner("http://localhost:8888/", t.TempDir())
	raw := runner.buildStreamURL(TestScenario{Name: "hls_case", Stream: StreamConfig{Format: "hls", Codec: "avc", Quality: "720p", Duration: 3 * time.Second}})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/hls/hls_case.m3u8" {
		t.Fatalf("HLS 地址错误: %s", raw)
	}
}

func TestRunScenarioRejectsUnsupportedFaults(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	runner := NewTestRunner(server.URL, t.TempDir())
	runner.Verbose = false
	result, err := runner.RunScenario(context.Background(), TestScenario{Name: "fault", Stream: StreamConfig{Format: "flv", Duration: time.Second}, Faults: []FaultConfig{{Type: "disconnect"}}, Expected: Expected{OutputPlayable: true}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "不支持") {
		t.Fatalf("未明确拒绝未实现的故障注入: %+v", result)
	}
	if calls != 0 {
		t.Fatalf("不支持的场景不应启动测试，请求次数 %d", calls)
	}
}

func validScenario() TestScenario {
	return TestScenario{Name: "basic", Stream: StreamConfig{Format: "flv", Codec: "avc", Quality: "480p", Duration: time.Second}, Expected: Expected{OutputPlayable: true, MinDuration: time.Second}}
}

func TestRunScenarioRejectsUnsupportedAssertions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TestRunner, *TestScenario)
	}{
		{"bgo_path", func(r *TestRunner, s *TestScenario) { r.BGOPath = "bgo" }},
		{"requires_bgo", func(r *TestRunner, s *TestScenario) { s.RequiresBGO = true }},
		{"reconnect", func(r *TestRunner, s *TestScenario) { s.Expected.DownloaderReconnects = true }},
		{"warnings", func(r *TestRunner, s *TestScenario) {
			s.Expected.ComplianceWarnings = []ComplianceWarning{{Type: "warning"}}
		}},
		{"size", func(r *TestRunner, s *TestScenario) { s.Expected.MaxFileSizeDiff = 0.1 }},
		{"unplayable", func(r *TestRunner, s *TestScenario) { s.Expected.OutputPlayable = false }},
		{"path", func(r *TestRunner, s *TestScenario) { s.Name = "../escape" }},
		{"duration", func(r *TestRunner, s *TestScenario) { s.Stream.Duration = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewTestRunner("http://127.0.0.1:1", t.TempDir())
			r.Verbose = false
			s := validScenario()
			tt.mutate(r, &s)
			result, err := r.RunScenario(context.Background(), s)
			if err != nil || result.Success || result.ErrorMessage == "" || result.OutputPath != "" {
				t.Fatalf("未明确拒绝: result=%+v err=%v", result, err)
			}
		})
	}
	for _, s := range GetAvailableScenarios() {
		if s.Name == "multi_stream_selection" && !s.RequiresBGO {
			t.Fatal("多流选择必须要求真实 bgo")
		}
	}
}

func TestParseMediaDuration(t *testing.T) {
	tests := []struct {
		name, body string
		valid      bool
	}{
		{"valid", `{"format":{"duration":"1.25"},"streams":[{"codec_type":"video"}]}`, true},
		{"audio", `{"format":{"duration":"2"},"streams":[{"codec_type":"audio"}]}`, true},
		{"empty", "", false},
		{"invalid_json", "oops", false},
		{"no_stream", `{"format":{"duration":"2"},"streams":[]}`, false},
		{"data_only", `{"format":{"duration":"2"},"streams":[{"codec_type":"data"}]}`, false},
	}
	for _, duration := range []string{"", "N/A", "NaN", "Inf", "-Inf", "-1", "0", "1e30", "0.0000000001", "1 trailing"} {
		tests = append(tests, struct {
			name, body string
			valid      bool
		}{duration, fmt.Sprintf(`{"format":{"duration":%q},"streams":[{"codec_type":"video"}]}`, duration), false})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMediaDuration([]byte(tt.body))
			if (err == nil) != tt.valid {
				t.Fatalf("duration=%v err=%v", got, err)
			}
			if tt.name == "valid" && got != 1250*time.Millisecond {
				t.Fatalf("小数时长丢失: %v", got)
			}
		})
	}
}

func helperRunner(t *testing.T, serverURL string) *TestRunner {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := NewTestRunner(serverURL, t.TempDir())
	r.Verbose = false
	r.commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, executable, append([]string{"-test.run=^TestRunnerHelperProcess$", "--", name}, args...)...)
		cmd.Env = append(os.Environ(), "TEST_RUNNER_HELPER=1")
		return cmd
	}
	return r
}

// TestRunnerHelperProcess 仅作为隔离的子进程模拟退出状态，不代表真实媒体测试。
func TestRunnerHelperProcess(t *testing.T) {
	if os.Getenv("TEST_RUNNER_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	if args[0] == "ffprobe" {
		helperWait("TEST_PROBE_WAIT")
		if os.Getenv("TEST_PROBE_FAIL") == "1" {
			os.Exit(1)
		}
		body := os.Getenv("TEST_PROBE_BODY")
		if body == "" {
			body = `{"format":{"duration":"1.5"},"streams":[{"codec_type":"video"}]}`
		}
		fmt.Print(body)
		os.Exit(0)
	}
	isDecode := false
	for _, a := range args {
		if a == "-xerror" {
			isDecode = true
		}
	}
	if isDecode {
		helperWait("TEST_DECODE_WAIT")
		hasAbort := false
		for i, a := range args {
			if a == "-abort_on" && i+1 < len(args) && args[i+1] == "empty_output" {
				hasAbort = true
			}
		}
		if !hasAbort {
			os.Exit(2)
		}
		if os.Getenv("TEST_DECODE_FAIL") == "1" {
			fmt.Fprint(os.Stderr, "invalid frame")
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("TEST_RECORD_WAIT") == "1" {
		time.Sleep(20 * time.Second)
	}
	if os.Getenv("TEST_RECORD_SKIP_OUTPUT") != "1" {
		if err := os.WriteFile(args[len(args)-1], []byte("test data"), 0600); err != nil {
			os.Exit(2)
		}
	}
	if os.Getenv("TEST_RECORD_FAIL") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

func healthServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(s.Close)
	return s
}

func TestRunScenarioRejectsProcessAndMediaFailures(t *testing.T) {
	for _, tt := range []struct{ name, key, value string }{
		{"record_exit", "TEST_RECORD_FAIL", "1"},
		{"probe_exit", "TEST_PROBE_FAIL", "1"},
		{"probe_malformed", "TEST_PROBE_BODY", "garbage"},
		{"decode_exit", "TEST_DECODE_FAIL", "1"},
		{"duration_short", "TEST_PROBE_BODY", `{"format":{"duration":"0.5"},"streams":[{"codec_type":"video"}]}`},
		{"missing_output", "TEST_RECORD_SKIP_OUTPUT", "1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)
			r := helperRunner(t, healthServer(t).URL)
			got, err := r.RunScenario(context.Background(), validScenario())
			if err != nil || got.Success || got.OutputPlayable || got.ErrorMessage == "" {
				t.Fatalf("不应通过: result=%+v err=%v", got, err)
			}
		})
	}
}

func TestRunScenarioUsesFreshOutput(t *testing.T) {
	r := helperRunner(t, healthServer(t).URL)
	a, err := r.RunScenario(context.Background(), validScenario())
	if err != nil || !a.Success {
		t.Fatalf("基线应通过: %+v err=%v", a, err)
	}
	b, err := r.RunScenario(context.Background(), validScenario())
	if err != nil || !b.Success || b.OutputPath == a.OutputPath {
		t.Fatalf("重复运行未隔离: %+v err=%v", b, err)
	}
	t.Setenv("TEST_RECORD_SKIP_OUTPUT", "1")
	c, err := r.RunScenario(context.Background(), validScenario())
	if err != nil || c.Success || c.OutputPlayable {
		t.Fatalf("旧产物导致假阳性: %+v err=%v", c, err)
	}
}

func TestRunScenarioCancellationFails(t *testing.T) {
	t.Setenv("TEST_RECORD_WAIT", "1")
	r := helperRunner(t, healthServer(t).URL)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	got, err := r.RunScenario(ctx, validScenario())
	if !errors.Is(err, context.DeadlineExceeded) || got.Success || got.OutputPlayable {
		t.Fatalf("取消被吞掉: %+v err=%v", got, err)
	}
}

func TestRunAllScenariosStopsOnCancellation(t *testing.T) {
	r := helperRunner(t, healthServer(t).URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := r.RunAllScenarios(ctx)
	if !errors.Is(err, context.Canceled) || len(results) != 1 || results[0].Success {
		t.Fatalf("取消后继续运行: results=%+v err=%v", results, err)
	}
}

func helperWait(key string) {
	if os.Getenv(key) != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("TEST_STARTED_PATH"), []byte("started"), 0600); err != nil {
		os.Exit(2)
	}
	time.Sleep(20 * time.Second)
}

func TestRunScenarioCancelsValidationProcesses(t *testing.T) {
	for _, stage := range []string{"TEST_PROBE_WAIT", "TEST_DECODE_WAIT"} {
		t.Run(stage, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "started")
			t.Setenv(stage, "1")
			t.Setenv("TEST_STARTED_PATH", marker)
			r := helperRunner(t, healthServer(t).URL)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type response struct {
				result *TestResult
				err    error
			}
			done := make(chan response, 1)
			go func() { result, err := r.RunScenario(ctx, validScenario()); done <- response{result, err} }()
			deadline := time.After(5 * time.Second)
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
		waitStarted:
			for {
				select {
				case <-deadline:
					t.Fatal("验证子进程未启动")
				case <-ticker.C:
					if _, err := os.Stat(marker); err == nil {
						break waitStarted
					}
				case got := <-done:
					t.Fatalf("子进程等待前测试已退出: %+v", got)
				}
			}
			cancel()
			select {
			case got := <-done:
				if !errors.Is(got.err, context.Canceled) || got.result.Success || got.result.OutputPlayable {
					t.Fatalf("验证取消被吞掉: %+v err=%v", got.result, got.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("取消后验证子进程仍未退出")
			}
		})
	}
}
