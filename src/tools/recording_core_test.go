package tools

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	tag(8, 0, []byte{0xaf, 0, 0x12, 0x10})
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
	// 当前测试进程充当外部工具，直接覆盖进程调用、校验、替换和发布。
	if mode := os.Getenv("BILILIVE_CORE_REPAIR_HELPER"); mode != "" {
		output := os.Args[len(os.Args)-1]
		if mode == "failure" {
			_ = os.WriteFile(output, []byte("partial"), 0600)
			os.Exit(1)
		}
		if mode == "cancel" {
			time.Sleep(time.Minute)
			os.Exit(0)
		}
		paths := []string{output}
		if strings.HasPrefix(mode, "multi") {
			paths = []string{output + ".one.flv", output + ".two.flv"}
		}
		for i, p := range paths {
			change := mode
			if mode == "multi-audio" && i == 1 {
				change = "audio"
			}
			data := coreFLV(false, change)
			if mode == "truncated" {
				data = data[:len(data)-1]
			}
			if err := os.WriteFile(p, data, 0600); err != nil {
				os.Exit(2)
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"Status": "OK", "Data": map[string]any{"OutputPaths": paths}})
		os.Exit(0)
	}
	for _, mode := range []string{"filler-only", "failure", "picture", "audio", "timestamp", "truncated", "cancel", "multi", "multi-audio", "multi-conflict"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "original.flv")
			multi := strings.HasPrefix(mode, "multi")
			original := coreFLV(!multi, "")
			if multi {
				original = append(original, coreFLV(false, "")[13:]...)
			}
			if err := os.WriteFile(input, original, 0600); err != nil {
				t.Fatal(err)
			}
			conflict := filepath.Join(dir, "original.part002.flv")
			if mode == "multi-conflict" {
				if err := os.WriteFile(conflict, []byte("existing"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			if mode == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			outputs, err := repairFLV(ctx, input, nil, func(_, output string) (*exec.Cmd, error) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestFLVRepairCore$", "--", output)
				cmd.Env = append(os.Environ(), "BILILIVE_CORE_REPAIR_HELPER="+mode)
				return cmd, nil
			})
			if mode == "multi" {
				if err != nil || len(outputs) != 2 {
					t.Fatalf("分段发布失败: %v %v", outputs, err)
				}
				if _, e := os.Stat(input); !os.IsNotExist(e) {
					t.Fatal("发布后仍保留原片")
				}
				for i, p := range outputs {
					want := filepath.Join(dir, fmt.Sprintf("original.part%03d.flv", i+1))
					got, e := os.ReadFile(p)
					if p != want || e != nil || !bytes.Equal(got, coreFLV(false, "")) {
						t.Fatal("发布路径或内容不正确")
					}
				}
				work, _ := filepath.Glob(filepath.Join(dir, ".bililive-fix-*"))
				if len(work) != 0 {
					t.Fatal("成功后工作目录未清理")
				}
				return
			}
			after, readErr := os.ReadFile(input)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "filler-only" {
				if err != nil || len(outputs) != 1 || outputs[0] != input || !bytes.Equal(after, coreFLV(false, mode)) {
					t.Fatalf("填充删除未安全替换: %v %v", outputs, err)
				}
			} else {
				if err == nil || !bytes.Equal(after, original) {
					t.Fatalf("异常未保留原片: %s %v", mode, err)
				}
				dirs, _ := filepath.Glob(filepath.Join(dir, ".bililive-fix-*"))
				if len(dirs) != 1 {
					t.Fatal("未保留诊断目录")
				}
				media, _ := filepath.Glob(filepath.Join(dirs[0], "*.flv"))
				if len(media) != 0 {
					t.Fatal("失败后残留修复视频")
				}
			}
			if mode == "multi-conflict" {
				got, _ := os.ReadFile(conflict)
				if string(got) != "existing" {
					t.Fatal("覆盖已有分段")
				}
				if _, e := os.Stat(filepath.Join(dir, "original.part001.flv")); !os.IsNotExist(e) {
					t.Fatal("失败后未撤销已发布分段")
				}
			}
		})
	}
}
