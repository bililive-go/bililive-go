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

// TestReadAppDataPathFromFile_MinimalParsing 测试最小解析，忽略其他不兼容字段
func TestReadAppDataPathFromFile_MinimalParsing(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yml")

	// 包含一个在旧版不支持的可能导致完整解析失败的字段，以及有效的 app_data_path
	content := `
app_data_path: "/test/appdata"
new_feature_xyz:
  enabled: true
  score: "invalid_type_for_int_in_full_config"
`
	err := os.WriteFile(configFile, []byte(content), 0644)
	assert.NoError(t, err)

	parsedPath, err := ReadAppDataPathFromFile(configFile)
	assert.NoError(t, err, "由于使用了最小子集结构体，其他不兼容字段不应导致解析失败")
	assert.Equal(t, "/test/appdata", parsedPath)

	// 测试回退到 out_put_path/.appdata
	content2 := `
out_put_path: "/test/out"
`
	err = os.WriteFile(configFile, []byte(content2), 0644)
	assert.NoError(t, err)

	parsedPath2, err := ReadAppDataPathFromFile(configFile)
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join("/test/out", ".appdata"), parsedPath2)
}
