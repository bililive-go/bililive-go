package iostats

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestBuildDownsamplePlanUsesAutoForLongRange(t *testing.T) {
	plan := buildDownsamplePlan(0, 7*24*60*60*1000, "raw")
	if plan.applied != "auto" {
		t.Fatalf("expected auto aggregation for long range, got %q", plan.applied)
	}
	if plan.bucketMs == 0 {
		t.Fatal("expected non-zero bucket for long range")
	}
}

func TestQueryIOStatsGlobalAggregatesLiveSeries(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	timestamp := int64(1_000)

	err := store.SaveIOStats(ctx, []*IOStat{
		{Timestamp: timestamp, StatType: StatTypeDiskRecordWrite, LiveID: "live-a", Platform: "bili", Speed: 10, TotalBytes: 100},
		{Timestamp: timestamp, StatType: StatTypeDiskRecordWrite, LiveID: "live-b", Platform: "bili", Speed: 20, TotalBytes: 200},
		{Timestamp: timestamp, StatType: StatTypeDiskRecordWrite, LiveID: "", Platform: "", Speed: 30, TotalBytes: 300},
	})
	if err != nil {
		t.Fatalf("save io stats failed: %v", err)
	}

	resp, err := store.QueryIOStats(ctx, IOStatsQuery{
		StartTime: timestamp,
		EndTime:   timestamp,
		StatTypes: []StatType{StatTypeDiskRecordWrite},
	})
	if err != nil {
		t.Fatalf("query io stats failed: %v", err)
	}

	if len(resp.Stats) != 1 {
		t.Fatalf("expected 1 global point, got %d", len(resp.Stats))
	}
	if resp.Stats[0].Speed != 30 {
		t.Fatalf("expected merged speed 30, got %d", resp.Stats[0].Speed)
	}
	if resp.Stats[0].LiveID != "" {
		t.Fatalf("expected global live_id, got %q", resp.Stats[0].LiveID)
	}
}

func TestQueryIOStatsGlobalKeepsNetworkSeries(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	timestamp := int64(2_000)

	err := store.SaveIOStats(ctx, []*IOStat{
		{Timestamp: timestamp, StatType: StatTypeNetworkDownload, LiveID: "", Platform: "", Speed: 40, TotalBytes: 400},
	})
	if err != nil {
		t.Fatalf("save io stats failed: %v", err)
	}

	resp, err := store.QueryIOStats(ctx, IOStatsQuery{
		StartTime: timestamp,
		EndTime:   timestamp,
		StatTypes: []StatType{StatTypeNetworkDownload},
	})
	if err != nil {
		t.Fatalf("query io stats failed: %v", err)
	}

	if len(resp.Stats) != 1 {
		t.Fatalf("expected 1 network point, got %d", len(resp.Stats))
	}
	if resp.Stats[0].Speed != 40 {
		t.Fatalf("expected network speed 40, got %d", resp.Stats[0].Speed)
	}
}

func TestQueryMemoryStatsDropsFlatStatsPayload(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	timestamp := int64(3_000)

	err := store.SaveMemoryStats(ctx, []*MemoryStat{
		{Timestamp: timestamp, Category: MemoryCategorySelf, RSS: 100, VMS: 200},
		{Timestamp: timestamp, Category: MemoryCategoryTotal, RSS: 300, VMS: 400},
	})
	if err != nil {
		t.Fatalf("save memory stats failed: %v", err)
	}

	resp, err := store.QueryMemoryStats(ctx, MemoryStatsQuery{
		StartTime: timestamp,
		EndTime:   timestamp,
	})
	if err != nil {
		t.Fatalf("query memory stats failed: %v", err)
	}

	if len(resp.GroupedStats) != 2 {
		t.Fatalf("expected 2 grouped categories, got %d", len(resp.GroupedStats))
	}
	if _, exists := resp.GroupedStats[MemoryCategorySelf]; !exists {
		t.Fatal("expected self category in grouped stats")
	}
	if len(resp.Stats) != 2 {
		t.Fatalf("expected 2 flat stats for compatibility, got %d", len(resp.Stats))
	}
}

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "iostats.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create sqlite store failed: %v", err)
	}

	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close sqlite store failed: %v", err)
		}
	})

	return store
}

func TestBuildDownsamplePlanRawShortRange(t *testing.T) {
	// 短时间范围 + raw 请求：应返回 raw（无需聚合）
	plan := buildDownsamplePlan(0, 60*1000, "raw")
	if plan.applied != "raw" {
		t.Fatalf("expected raw for short range, got %q", plan.applied)
	}
	if plan.bucketMs != 0 {
		t.Fatalf("expected zero bucket for raw short range, got %d", plan.bucketMs)
	}
}

func TestBuildDownsamplePlanMinuteRequest(t *testing.T) {
	// minute 请求 + 短范围：应用 minute 聚合
	plan := buildDownsamplePlan(0, 60*60*1000, "minute")
	if plan.applied != "minute" {
		t.Fatalf("expected minute, got %q", plan.applied)
	}
	if plan.bucketMs != 60*1000 {
		t.Fatalf("expected 60000ms bucket, got %d", plan.bucketMs)
	}
}

func TestBuildDownsamplePlanAutoOverridesMinute(t *testing.T) {
	// 超长时间范围 + minute 请求：auto 应覆盖 minute
	plan := buildDownsamplePlan(0, 30*24*60*60*1000, "minute")
	if plan.applied != "auto" {
		t.Fatalf("expected auto to override minute for very long range, got %q", plan.applied)
	}
	if plan.bucketMs <= 60*1000 {
		t.Fatalf("expected bucket larger than minute, got %d", plan.bucketMs)
	}
}

func TestNormalizeAggregation(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "auto"},
		{"auto", "auto"},
		{"AUTO", "auto"},
		{"raw", "raw"},
		{"none", "raw"},
		{"minute", "minute"},
		{"hour", "hour"},
		{"invalid", "auto"},
		{"  auto  ", "auto"},
	}
	for _, tt := range tests {
		got := normalizeAggregation(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeAggregation(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestQueryIOStatsLiveFiltered(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()
	timestamp := int64(4_000)

	err := store.SaveIOStats(ctx, []*IOStat{
		{Timestamp: timestamp, StatType: StatTypeDiskRecordWrite, LiveID: "live-a", Platform: "bili", Speed: 10, TotalBytes: 100},
		{Timestamp: timestamp, StatType: StatTypeDiskRecordWrite, LiveID: "live-b", Platform: "bili", Speed: 20, TotalBytes: 200},
		{Timestamp: timestamp, StatType: StatTypeDiskRecordWrite, LiveID: "", Platform: "", Speed: 30, TotalBytes: 300},
	})
	if err != nil {
		t.Fatalf("save io stats failed: %v", err)
	}

	resp, err := store.QueryIOStats(ctx, IOStatsQuery{
		StartTime: timestamp,
		EndTime:   timestamp,
		StatTypes: []StatType{StatTypeDiskRecordWrite},
		LiveID:    "live-a",
	})
	if err != nil {
		t.Fatalf("query io stats failed: %v", err)
	}

	if len(resp.Stats) != 1 {
		t.Fatalf("expected 1 point for live-a, got %d", len(resp.Stats))
	}
	if resp.Stats[0].Speed != 10 {
		t.Fatalf("expected speed 10, got %d", resp.Stats[0].Speed)
	}
	if resp.Stats[0].LiveID != "live-a" {
		t.Fatalf("expected live_id live-a, got %q", resp.Stats[0].LiveID)
	}
}

func TestQueryIOStatsDownsampleMergesBuckets(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	// 写入同一分钟内的多个数据点（间隔 5s）
	err := store.SaveIOStats(ctx, []*IOStat{
		{Timestamp: 0, StatType: StatTypeNetworkDownload, Speed: 100},
		{Timestamp: 5000, StatType: StatTypeNetworkDownload, Speed: 200},
		{Timestamp: 10000, StatType: StatTypeNetworkDownload, Speed: 300},
		{Timestamp: 55000, StatType: StatTypeNetworkDownload, Speed: 400},
	})
	if err != nil {
		t.Fatalf("save io stats failed: %v", err)
	}

	resp, err := store.QueryIOStats(ctx, IOStatsQuery{
		StartTime:   0,
		EndTime:     55000,
		StatTypes:   []StatType{StatTypeNetworkDownload},
		Aggregation: "minute",
	})
	if err != nil {
		t.Fatalf("query io stats failed: %v", err)
	}

	// 4 个点在 0~55000 范围内，minute bucket = 60000ms，应合并为 1 个桶
	if len(resp.Stats) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(resp.Stats))
	}
	// AVG(100, 200, 300, 400) = 250
	if resp.Stats[0].Speed != 250 {
		t.Fatalf("expected avg speed 250, got %d", resp.Stats[0].Speed)
	}
	if resp.AppliedAggregation != "minute" {
		t.Fatalf("expected applied_aggregation minute, got %q", resp.AppliedAggregation)
	}
}

func TestQueryIOStatsDownsampleKeepsLatestTotalBytes(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	err := store.SaveIOStats(ctx, []*IOStat{
		{Timestamp: 0, StatType: StatTypeDiskRecordWrite, LiveID: "live-a", Platform: "bili", Speed: 100, TotalBytes: 900},
		{Timestamp: 5000, StatType: StatTypeDiskRecordWrite, LiveID: "live-a", Platform: "bili", Speed: 50, TotalBytes: 50},
	})
	if err != nil {
		t.Fatalf("save io stats failed: %v", err)
	}

	resp, err := store.QueryIOStats(ctx, IOStatsQuery{
		StartTime:   0,
		EndTime:     5000,
		StatTypes:   []StatType{StatTypeDiskRecordWrite},
		LiveID:      "live-a",
		Aggregation: "minute",
	})
	if err != nil {
		t.Fatalf("query io stats failed: %v", err)
	}

	if len(resp.Stats) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(resp.Stats))
	}
	if resp.Stats[0].TotalBytes != 50 {
		t.Fatalf("expected latest total_bytes 50, got %d", resp.Stats[0].TotalBytes)
	}
}

func TestNewSQLiteStoreRebuildsDirtyDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "iostats.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create sqlite store failed: %v", err)
	}

	ctx := context.Background()
	err = store.SaveIOStats(ctx, []*IOStat{
		{Timestamp: 1_000, StatType: StatTypeNetworkDownload, Speed: 123, TotalBytes: 456},
	})
	if err != nil {
		t.Fatalf("save io stats failed: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close sqlite store failed: %v", err)
	}

	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite failed: %v", err)
	}
	_, err = rawDB.Exec(`UPDATE schema_migrations SET dirty = 1`)
	if err != nil {
		rawDB.Close()
		t.Fatalf("mark dirty failed: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw sqlite failed: %v", err)
	}

	store, err = NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store failed: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close sqlite store failed: %v", err)
		}
	}()

	resp, err := store.QueryIOStats(ctx, IOStatsQuery{
		StartTime: 0,
		EndTime:   10_000,
		StatTypes: []StatType{StatTypeNetworkDownload},
	})
	if err != nil {
		t.Fatalf("query io stats failed: %v", err)
	}
	if len(resp.Stats) != 0 {
		t.Fatalf("expected rebuilt database to be empty, got %d points", len(resp.Stats))
	}

	matches, err := filepath.Glob(dbPath + ".dirty-*")
	if err != nil {
		t.Fatalf("glob dirty archive failed: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected dirty database archive to be created")
	}
}

func TestSplitGlobalStatTypesEmpty(t *testing.T) {
	globalOnly, aggregated := splitGlobalStatTypes(nil)
	// 空列表应 fallback 到所有可查询类型
	if len(globalOnly) == 0 && len(aggregated) == 0 {
		t.Fatal("expected non-empty results when passing nil stat types")
	}
	// network_download 应在 globalOnly 中
	found := false
	for _, st := range globalOnly {
		if st == StatTypeNetworkDownload {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected network_download in globalOnly types")
	}
}
