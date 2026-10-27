package stages

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/sirupsen/logrus"
)

func TestFixFailureFallback(t *testing.T) {
	for _, mode := range []string{"readable", "broken", "missing", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.flv")
			// 一条 AAC 数据标签，验证容器检查而非解码。
			data := []byte{'F', 'L', 'V', 1, 4, 0, 0, 0, 9, 0, 0, 0, 0, 8, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0xaf, 1, 42, 0, 0, 0, 14}
			if mode == "broken" {
				data = []byte("broken")
			}
			if mode != "missing" {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			stage := &FixFlvStage{repair: func(context.Context, string) ([]string, error) { return nil, errors.New("tool failed") }}
			out, err := stage.Execute(&pipeline.PipelineContext{Ctx: ctx, Logger: livelogger.New(10, logrus.Fields{})}, []pipeline.FileInfo{{Path: path, Type: pipeline.FileTypeVideo, Deletable: true}})
			if mode != "readable" {
				if err == nil {
					t.Fatal("must fail")
				}
				return
			}
			if err != nil || len(out) != 1 || out[0].Path != path || out[0].Deletable {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if out[0].Metadata["preserve_original"] != true || !strings.Contains(stage.GetLogs(), "警告") {
				t.Fatal("missing warning/protection")
			}
			del := &DeleteSourceStage{}
			remaining, err := del.Execute(&pipeline.PipelineContext{Ctx: ctx, Logger: livelogger.New(10, logrus.Fields{})}, out)
			if err != nil || len(remaining) != 1 {
				t.Fatalf("protected source deleted: %v", err)
			}
			if _, err = os.Stat(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}
