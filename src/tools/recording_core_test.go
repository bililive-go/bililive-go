package tools

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/bilibili"
	"github.com/hr3lxphr6j/requests"
)

type coreTransport func(*http.Request) (*http.Response, error)

func (f coreTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBilibili412KeepsLiveStatus(t *testing.T) {
	l := &bilibili.Live{}
	l.Url, _ = url.Parse("https://live.bilibili.com/123")
	l.Options = live.MustNewOptions()
	userRequests := 0
	l.RequestSession = requests.NewSession(&http.Client{Transport: coreTransport(func(r *http.Request) (*http.Response, error) {
		status, body := 200, `{"code":0,"data":{"room_id":123,"title":"test","live_status":1}}`
		if strings.Contains(r.URL.Path, "get_anchor_in_room") {
			status = 412
			body = "blocked"
			userRequests++
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})})
	for range 2 {
		info, err := l.GetInfo()
		if err != nil || !info.Status || info.HostName != "房间123" {
			t.Fatalf("412 阻断了开播检测: info=%v err=%v", info, err)
		}
	}
	if userRequests != 1 {
		t.Fatalf("失败后没有退避: requests=%d", userRequests)
	}
}

// 仅构造容器与编码数据检查所需的最小样本，不依赖真实录像或解码工具。
func coreFLV(filler bool, change string) []byte {
	data := []byte{'F', 'L', 'V', 1, 5, 0, 0, 0, 9, 0, 0, 0, 0}
	tag := func(kind byte, ts uint32, payload []byte) {
		n := len(payload)
		data = append(data, kind, byte(n>>16), byte(n>>8), byte(n), byte(ts>>16), byte(ts>>8), byte(ts), byte(ts>>24), 0, 0, 0)
		data = append(data, payload...)
		var prev [4]byte
		binary.BigEndian.PutUint32(prev[:], uint32(n+11))
		data = append(data, prev[:]...)
	}
	tag(9, 0, []byte{0x17, 0, 0, 0, 0, 1, 100, 0, 31, 0xff, 0xe1, 0, 4, 0x67, 100, 0, 31, 1, 0, 2, 0x68, 1})
	for i := 0; i < 2; i++ {
		audio := []byte{0xaf, 1, 42}
		if change == "audio" {
			audio[2]++
		}
		tag(8, uint32(i*1000), audio)
		video := []byte{0x27, 1, 0, 0, 0, 0, 0, 0, 3, 0x41, byte(i), 0x88}
		if change == "picture" {
			video[len(video)-1]++
		}
		if filler {
			video = append(video, 0, 0, 0, 122, 12)
			video = append(video, bytes.Repeat([]byte{0xff}, 120)...)
			video = append(video, 0x80)
		}
		timestamp := uint32(i * 1000)
		if change == "timestamp" {
			timestamp++
		}
		tag(9, timestamp, video)
	}
	return data
}

func TestFLVRepairCore(t *testing.T) {
	// 当前测试进程充当隔离的修复工具，覆盖实际进程调用与替换流程。
	if mode := os.Getenv("BILILIVE_CORE_REPAIR_HELPER"); mode != "" {
		output := os.Args[len(os.Args)-1]
		if mode == "failure" {
			_ = os.WriteFile(output, []byte("partial"), 0600)
			os.Exit(1)
		}
		if err := os.WriteFile(output, coreFLV(false, mode), 0600); err != nil {
			os.Exit(2)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"Status": "OK", "Data": map[string]any{"OutputFiles": []string{output}}})
		os.Exit(0)
	}
	for _, mode := range []string{"filler-only", "failure", "picture", "audio", "timestamp"} {
		t.Run(mode, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "original.flv")
			original := coreFLV(true, "")
			if err := os.WriteFile(input, original, 0600); err != nil {
				t.Fatal(err)
			}
			outputs, err := repairFLV(context.Background(), input, nil, func(_, output string) (*exec.Cmd, error) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestFLVRepairCore$", "--", output)
				cmd.Env = append(os.Environ(), "BILILIVE_CORE_REPAIR_HELPER="+mode)
				return cmd, nil
			})
			after, readErr := os.ReadFile(input)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "filler-only" {
				if err != nil || len(outputs) != 1 || outputs[0] != input || !bytes.Equal(after, coreFLV(false, mode)) {
					t.Fatalf("合法填充删除未安全替换: outputs=%v err=%v", outputs, err)
				}
			} else if err == nil || !bytes.Equal(after, original) {
				t.Fatalf("异常修复未保留原片: mode=%s err=%v", mode, err)
			}
		})
	}
}
