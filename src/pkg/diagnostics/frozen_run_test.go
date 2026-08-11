package diagnostics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFrozenRunTestManager(t *testing.T, appDataPath string) *Manager {
	t.Helper()
	manager, err := newManager(Config{
		AppDataPath:       appDataPath,
		AppVersion:        "frozen-run-test",
		HeartbeatInterval: time.Hour,
		EventSyncInterval: time.Hour,
		Flight: FlightConfig{
			Enabled: false,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = manager.Abort()
	})
	return manager
}

func TestFrozenRunReleasesSubsystemLocksAndKeepsFixedEventBoundary(t *testing.T) {
	manager := newFrozenRunTestManager(t, t.TempDir())
	manager.Record(context.Background(), "test.before.freeze", Fields{
		"component": "test",
	})

	snapshot, err := manager.freezeRun(
		context.Background(),
		manager.RunID(),
		true,
		false,
	)
	require.NoError(t, err)
	defer snapshot.Close()

	require.True(t, manager.eventMu.TryLock(), "冻结返回后不能继续占用 eventMu")
	manager.eventMu.Unlock()
	require.True(t, manager.flight.mu.TryLock(), "冻结返回后不能继续占用 flight.mu")
	manager.flight.mu.Unlock()
	require.True(t, manager.livenessMu.TryLock(), "冻结返回后不能继续占用 livenessMu")
	manager.livenessMu.Unlock()

	frozenBefore, err := loadFrozenEvents(context.Background(), snapshot)
	require.NoError(t, err)
	manager.Record(context.Background(), "test.after.freeze", Fields{
		"component": "test",
	})
	frozenAfter, err := loadFrozenEvents(context.Background(), snapshot)
	require.NoError(t, err)
	assert.Len(t, frozenAfter.Events, len(frozenBefore.Events))

	liveEvents, err := loadEvents(manager.runDir)
	require.NoError(t, err)
	assert.Greater(t, len(liveEvents.Events), len(frozenAfter.Events))
}

func TestExportQueueHonorsCanceledContextWithoutCreatingArtifact(t *testing.T) {
	manager := newFrozenRunTestManager(t, t.TempDir())
	release, err := manager.acquireExport(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, buildErr := manager.BuildViewerBundleContext(ctx, manager.RunID())
		done <- buildErr
	}()
	cancel()

	select {
	case buildErr := <-done:
		require.ErrorIs(t, buildErr, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("已取消的导出仍阻塞在排队锁上")
	}
	release()

	entries, err := os.ReadDir(manager.exportsDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestExportRejectsRunStillOwnedByAnotherManager(t *testing.T) {
	appDataPath := t.TempDir()
	owner := newFrozenRunTestManager(t, appDataPath)
	reader := newFrozenRunTestManager(t, appDataPath)

	_, err := reader.BuildArchiveContext(context.Background(), owner.RunID())
	require.ErrorIs(t, err, ErrRunActive)
	_, statErr := os.Stat(filepath.Join(
		appDataPath,
		"diagnostics",
		"runs",
		owner.RunID(),
	))
	require.NoError(t, statErr)
}

func TestFrozenMetadataRejectsTrailingGarbage(t *testing.T) {
	manager := newFrozenRunTestManager(t, t.TempDir())
	path := filepath.Join(manager.runDir, "clean.json")
	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"schema":"bililive.diagnostics-clean/v1","run_id":"`+
			manager.RunID()+`","created_at":"2026-07-30T00:00:00Z","final_seq":1}`+
			` trailing`),
		0o600,
	))

	snapshot, err := openFrozenRunFiles(
		context.Background(),
		manager.runDir,
		manager.RunID(),
		false,
		false,
		frozenRootFiles,
	)
	require.NoError(t, err)
	defer snapshot.Close()

	var marker cleanMarker
	err = snapshot.readMetadata("clean.json", &marker)
	require.Error(t, err)
	assert.False(t, errors.Is(err, os.ErrNotExist))
}
