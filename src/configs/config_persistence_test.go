package configs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bililive-go/bililive-go/src/types"
	"github.com/stretchr/testify/assert"
)

func TestPersistence(t *testing.T) {
	// 1. Setup temp config file
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")
	initialContent := `
rpc:
  enable: true
debug: false
live_rooms:
  - url: http://live.bilibili.com/123
`
	err := os.WriteFile(configFile, []byte(initialContent), 0644)
	assert.NoError(t, err)

	// 2. Load config
	cfg, err := NewConfigWithFile(configFile)
	assert.NoError(t, err)
	SetCurrentConfig(cfg)

	// 3. Test Persistent Update (SetDebug)
	t.Log("Testing Persistent Update: SetDebug")
	statBefore, err := os.Stat(configFile)
	assert.NoError(t, err)
	time.Sleep(100 * time.Millisecond) // Ensure mtime difference if FS has low resolution

	SetDebug(true)

	// Check memory
	assert.True(t, GetCurrentConfig().Debug)

	// Check file
	contentAfter, err := os.ReadFile(configFile)
	assert.NoError(t, err)
	assert.True(t, strings.Contains(string(contentAfter), "debug: true"), "File should contain debug: true")

	statAfter, err := os.Stat(configFile)
	assert.NoError(t, err)
	assert.True(t, statAfter.ModTime().After(statBefore.ModTime()), "File mtime should be updated")

	// 4. Test Transient Update (SetLiveRoomId)
	t.Log("Testing Transient Update: SetLiveRoomId")
	statBeforeTransient, err := os.Stat(configFile)
	assert.NoError(t, err)

	// Wait to ensure we can distinguish mtime if it were to change
	time.Sleep(100 * time.Millisecond)

	fakeID := types.LiveID("fake_id_123")
	SetLiveRoomId("http://live.bilibili.com/123", fakeID)

	// Check memory
	current := GetCurrentConfig()
	room, err := current.GetLiveRoomByUrl("http://live.bilibili.com/123")
	assert.NoError(t, err)
	assert.Equal(t, fakeID, room.LiveId)

	// Check file (Should NOT change)
	statAfterTransient, err := os.Stat(configFile)
	assert.NoError(t, err)
	assert.Equal(t, statBeforeTransient.ModTime(), statAfterTransient.ModTime(), "File mtime should NOT be updated for transient change")

	contentAfterTransient, err := os.ReadFile(configFile)
	assert.NoError(t, err)
	assert.Equal(t, string(contentAfter), string(contentAfterTransient), "File content should not change")

}
