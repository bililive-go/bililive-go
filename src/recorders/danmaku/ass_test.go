package danmaku

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
)

// TestAssignLaneDelayIsBounded 验证弹幕高峰期间排队延迟不会无限累积
// (issue #1178：录制时长 1 分钟但弹幕过多时，生成的 ass 文件时间轴长达 5 分钟)。
func TestAssignLaneDelayIsBounded(t *testing.T) {
	cfg := configs.DanmakuConfig{}
	cfg.SetDefaults()
	cfg.ScrollArea = "quarter" // 缩小可用 lane 数量，更容易触发排队

	path := filepath.Join(t.TempDir(), "test.ass")
	w, err := NewAssWriter(path, time.Now(), cfg, "test")
	if err != nil {
		t.Fatalf("NewAssWriter failed: %v", err)
	}
	defer w.Close()

	// 模拟弹幕高峰：到达速度远快于屏幕可承载速度（startCS 每条只增加 1 厘秒）
	for i := 0; i < 2000; i++ {
		startCS := int64(i)
		_, adjustedStart := w.assignLane(startCS, 200)
		if delay := adjustedStart - startCS; delay > w.maxLaneDelayCS {
			t.Fatalf("danmaku #%d: lane queueing delay exceeded cap: got %d cs, want <= %d cs", i, delay, w.maxLaneDelayCS)
		}
	}
}
