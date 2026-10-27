package tools

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleFLV(count int, video bool) []byte {
	data := []byte{'F', 'L', 'V', 1, 5, 0, 0, 0, 9, 0, 0, 0, 0}
	for i := 0; i < count; i++ {
		types := []byte{8}
		if video {
			types = append(types, 9)
		}
		for _, kind := range types {
			payload := []byte{0xaf, 1, 0, 0, 0, 42}
			if kind == 9 {
				payload = []byte{0x17, 1, 0, 0, 0, 42}
			}
			ts := uint32(i * 1000)
			tag := []byte{kind, 0, 0, byte(len(payload)), byte(ts >> 16), byte(ts >> 8), byte(ts), byte(ts >> 24), 0, 0, 0}
			data = append(data, tag...)
			data = append(data, payload...)
			var prev [4]byte
			binary.BigEndian.PutUint32(prev[:], uint32(len(payload)+11))
			data = append(data, prev[:]...)
		}
	}
	return data
}

// 使用当前测试进程作为外部工具，覆盖退出、路径和内容异常，不依赖 shell。
func TestRepairHelper(t *testing.T) {
	if os.Getenv("FLV_REPAIR_HELPER") != "1" {
		return
	}
	args := os.Args
	input, output, mode := args[len(args)-3], args[len(args)-2], args[len(args)-1]
	data, _ := os.ReadFile(input)
	switch mode {
	case "filler-only", "filler-audio", "filler-picture", "filler-config", "filler-timestamp", "filler-cts", "filler-drop", "filler-metadata":
		mutation := strings.TrimPrefix(mode, "filler-")
		if mutation == "only" {
			mutation = ""
		}
		data = fillerFixture(fillerFixtureOptions{mutation: mutation})
	case "exit":
		_ = os.WriteFile(output, []byte("partial"), 0600)
		os.Stderr.WriteString("simulated disk error")
		os.Exit(1)
	case "cancel":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "small":
		data = sampleFLV(5, true)
	case "audio-missing":
		for pos := 13; pos < len(data); pos += 21 {
			if data[pos] == 8 {
				data[pos] = 18
			}
		}
	case "success":
		data[29] = 43
	case "truncated":
		data = data[:len(data)-2]
	case "symlink":
		if err := os.Symlink(input, output); err != nil {
			os.Exit(2)
		}
	case "json":
		os.Stdout.WriteString("not json")
		os.Exit(0)
	case "missing":
		output = filepath.Join(filepath.Dir(output), "missing.flv")
	case "escape":
		output = input
	}
	if mode != "escape" && mode != "symlink" && mode != "missing" {
		_ = os.WriteFile(output, data, 0600)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"Status": "OK", "Data": map[string]any{"OutputFiles": []string{output}}})
	os.Exit(0)
}
func TestRepairPreservesOriginalOnFailure(t *testing.T) {
	for _, mode := range []string{"exit", "small", "audio-missing", "truncated", "escape", "symlink", "json", "missing", "success", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "原片.flv")
			original := sampleFLV(100, true)
			if err := os.WriteFile(input, original, 0600); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if mode == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			out, err := repairFLV(ctx, input, nil, func(in, dest string) (*exec.Cmd, error) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestRepairHelper$", "--", in, dest, mode)
				cmd.Env = append(os.Environ(), "FLV_REPAIR_HELPER=1")
				return cmd, nil
			})
			if mode == "success" {
				if err != nil || len(out) != 1 || out[0] != input {
					t.Fatalf("%v %v", out, err)
				}
			} else if err == nil {
				t.Fatal("expected failure")
			}
			after, e := os.ReadFile(input)
			if mode == "success" {
				original[29] = 43
			}
			if e != nil || !bytes.Equal(after, original) {
				t.Fatalf("original lost or changed: %v", e)
			}
			if mode == "exit" && !strings.Contains(err.Error(), "simulated disk error") {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if entry.IsDir() {
					files, _ := filepath.Glob(filepath.Join(dir, entry.Name(), "*.flv"))
					if len(files) != 0 {
						t.Fatalf("partial outputs retained: %v", files)
					}
					if _, e := os.Stat(filepath.Join(dir, entry.Name(), "stderr.log")); e != nil {
						t.Fatal(e)
					}
				}
			}
		})
	}
}
