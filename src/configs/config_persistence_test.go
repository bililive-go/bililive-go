package configs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	_, err = SetDebug(true)
	assert.NoError(t, err)

	// Check memory
	assert.True(t, GetCurrentConfig().Debug)

	// Check file
	contentAfter, err := os.ReadFile(configFile)
	assert.NoError(t, err)
	assert.True(t, strings.Contains(string(contentAfter), "debug: true"), "File should contain debug: true")

	// 4. Test Transient Update (SetLiveRoomId)
	t.Log("Testing Transient Update: SetLiveRoomId")

	fakeID := types.LiveID("fake_id_123")
	_, err = SetLiveRoomId("http://live.bilibili.com/123", fakeID)
	assert.NoError(t, err)

	// Check memory
	current := GetCurrentConfig()
	room, err := current.GetLiveRoomByUrl("http://live.bilibili.com/123")
	assert.NoError(t, err)
	assert.Equal(t, fakeID, room.LiveId)

	// Check file (Should NOT change)
	contentAfterTransient, err := os.ReadFile(configFile)
	assert.NoError(t, err)
	assert.Equal(t, string(contentAfter), string(contentAfterTransient), "File content should not change")

}
