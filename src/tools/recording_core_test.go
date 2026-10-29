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

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/bilibili"
	"github.com/hr3lxphr6j/requests"
	"github.com/stretchr/testify/require"
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

func TestSavedAudioModeAppliesWithoutChangingActiveStream(t *testing.T) {
	previous := configs.GetCurrentConfig()
	t.Cleanup(func() { configs.SetCurrentConfig(previous) })
	l := &bilibili.Live{}
	l.Url, _ = url.Parse("https://live.bilibili.com/123")
	l.Options = live.MustNewOptions()
	configs.SetCurrentConfig(&configs.Config{LiveRooms: []configs.LiveRoom{{Url: l.GetRawUrl()}}})
	activeMode := live.RecordingAudioOnly(l, nil)
	activeStream := &live.StreamUrlInfo{RecordingAudioOnly: &activeMode}
	for _, enabled := range []bool{true, false, true} {
		_, err := configs.UpdateWithRetryTransient(func(c *configs.Config) error {
			c.LiveRooms[0].AudioOnly = enabled
			return nil
		}, 3, time.Millisecond)
		require.NoError(t, err)
		require.Equal(t, enabled, live.RecordingAudioOnly(l, nil), "下一轮必须使用已保存配置")
		require.False(t, live.RecordingAudioOnly(l, activeStream), "当前取流快照不能被修改")
		require.False(t, l.Options.AudioOnly, "不并发修改平台共享选项")
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
		audioTime := uint32(i * 1000)
		if change == "jump" && i > 0 {
			audioTime += 3600000
		}
		if change != "drop" || i != 0 {
			tag(8, audioTime, audio)
		}
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
		if change == "jump" && i > 0 {
			timestamp += 3600000
		}
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
			if mode == "jump" {
				change = ""
			}
			if mode == "jump-audio" {
				change = "audio"
			}
			if mode == "multi-audio" && i == 1 {
				change = "audio"
			}
			data := coreFLV(false, change)
			if strings.HasPrefix(mode, "normal-") {
				change = strings.TrimPrefix(mode, "normal-")
				if change == "filler" {
					change = "pass"
				}
				change = strings.TrimPrefix(change, "filler-")
				data = coreNormalFLV(change)
			}
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
	successes := map[string][]string{
		"filler-only": {"original.flv"}, "jump": {"original.flv"},
		"normal-pass": {"original.flv"}, "normal-filler": {"original.flv"},
		"source-tail": {"original.repaired.flv"}, "source-prev": {"original.repaired.flv"},
		"multi":        {"original.part001.flv", "original.part002.flv"},
		"multi-resume": {"original.part001.flv", "original.part002.flv"},
	}
	for _, mode := range []string{"filler-only", "failure", "picture", "audio", "timestamp", "truncated", "cancel", "multi", "multi-resume", "multi-resume-conflict", "multi-audio", "multi-conflict", "normal-pass", "normal-audio", "normal-picture", "normal-drop", "normal-filler", "normal-filler-picture", "source-tail", "source-prev", "jump", "jump-audio"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "original.flv")
			multi := strings.HasPrefix(mode, "multi")
			original := coreFLV(!multi && !strings.HasPrefix(mode, "source-") && !strings.HasPrefix(mode, "jump"), "")
			if mode == "source-tail" {
				original = append(original, 9, 0, 0, 100, 0, 0, 0, 0, 0, 0, 0, 0x17)
			}
			if mode == "source-prev" {
				original[12] = 1
			}
			if strings.HasPrefix(mode, "jump") {
				original = coreFLV(false, "jump")
			}
			if strings.HasPrefix(mode, "normal-") {
				original = coreNormalFLV("pass")
				if strings.Contains(mode, "filler") {
					original = coreNormalFLV("filler")
				}
			}
			if multi {
				original = append(original, coreFLV(false, "")[13:]...)
			}
			require.NoError(t, os.WriteFile(input, original, 0600))
			conflict := filepath.Join(dir, "original.part002.flv")
			if mode == "multi-conflict" || mode == "multi-resume-conflict" {
				require.NoError(t, os.WriteFile(conflict, []byte("existing"), 0600))
			}
			if strings.HasPrefix(mode, "multi-resume") {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "original.part001.flv"), coreFLV(false, ""), 0600))
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
			wantPaths, success := successes[mode]
			work, globErr := filepath.Glob(filepath.Join(dir, ".bililive-fix-*"))
			require.NoError(t, globErr)
			if success {
				require.NoError(t, err)
				require.Len(t, outputs, len(wantPaths))
				want := coreFLV(false, "")
				if strings.HasPrefix(mode, "normal-") {
					want = coreNormalFLV("pass")
				}
				for i, name := range wantPaths {
					require.Equal(t, filepath.Join(dir, name), outputs[i])
					coreCheckFile(t, outputs[i], want)
				}
				if multi {
					require.NoFileExists(t, input)
				} else if strings.HasPrefix(mode, "source-") {
					coreCheckFile(t, input, original)
				}
				require.Empty(t, work, "成功后工作目录未清理")
			} else {
				require.Error(t, err)
				coreCheckFile(t, input, original)
				require.Len(t, work, 1, "失败后诊断目录未保留")
				media, e := filepath.Glob(filepath.Join(work[0], "*.flv"))
				require.NoError(t, e)
				require.Empty(t, media, "失败后残留修复视频")
			}
			if mode == "jump-audio" {
				require.ErrorContains(t, err, "content")
			}
			if strings.HasSuffix(mode, "conflict") {
				coreCheckFile(t, conflict, []byte("existing"))
				first := filepath.Join(dir, "original.part001.flv")
				if mode == "multi-resume-conflict" {
					coreCheckFile(t, first, coreFLV(false, ""))
				} else {
					require.NoFileExists(t, first, "未撤销本次创建的分段")
				}
			}
		})
	}
}

func coreCheckFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, bytes.Equal(got, want), "文件内容改变: %s", path)
}

// 100 个音视频包，丢一个音频包仍超过旧的 98% 阈值，必须由内容比较拒绝。
func coreNormalFLV(change string) []byte {
	data := coreFLV(change == "filler", "")
	for i := 1; i < 50; i++ {
		kind := ""
		if i == 49 {
			kind = change
		}
		data = append(data, coreFLV(false, kind)[13:]...)
	}
	return data
}

// 子进程直接模拟解码工具，不依赖 shell 或外部 FFmpeg。
func TestMain(m *testing.M) {
	if mode := os.Getenv("BILILIVE_CORE_AUDIO_HELPER"); mode != "" {
		switch mode {
		case "valid":
			fmt.Print("decoded PCM")
		case "invalid":
			os.Exit(1)
		case "cancel":
			time.Sleep(time.Minute)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestAudioMediaCore(t *testing.T) {
	for _, mode := range []string{"valid", "header-only", "invalid", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("BILILIVE_CORE_AUDIO_HELPER", mode)
			ctx := context.Background()
			if mode == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			valid, err := HasDecodableAudio(ctx, os.Args[0], "local.aac")
			if mode == "cancel" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				return
			}
			require.NoError(t, err)
			require.Equal(t, mode == "valid", valid, "音频可用性误判")
		})
	}
}
