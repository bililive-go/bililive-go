package configs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewConfig(t *testing.T) {
	file := "../../config.yml"
	c, err := NewConfigWithFile("../../config.yml")
	assert.NoError(t, err)
	assert.Equal(t, file, c.File)
}

func TestRPC_Verify(t *testing.T) {
	var rpc *RPC
	assert.NoError(t, rpc.verify())
	rpc = new(RPC)
	rpc.Bind = "foo@bar"
	assert.NoError(t, rpc.verify())
	rpc.Enable = true
	assert.Error(t, rpc.verify())
}

func TestConfig_Verify(t *testing.T) {
	var cfg *Config
	assert.Error(t, cfg.Verify())
	cfg = &Config{
		RPC:        defaultRPC,
		Interval:   30,
		OutPutPath: os.TempDir(),
	}
	assert.NoError(t, cfg.Verify())
	cfg.Interval = 0
	assert.Error(t, cfg.Verify())
	cfg.Interval = 30
	cfg.OutPutPath = "foobar"
	assert.Error(t, cfg.Verify())
	cfg.OutPutPath = os.TempDir()
	cfg.RPC.Enable = false
	assert.Error(t, cfg.Verify())
}

func TestResolveConfigForRoom(t *testing.T) {
	cfg := &Config{
		Interval:   60,
		OutPutPath: "/global",
		FfmpegPath: "/usr/bin/ffmpeg",
		PlatformConfigs: map[string]PlatformConfig{
			"douyin": {
				OverridableConfig: OverridableConfig{
					Interval:   intPtr(30),
					OutPutPath: stringPtr("/douyin"),
				},
			},
		},
	}

	room := &LiveRoom{
		Url: "https://live.douyin.com/123456",
		OverridableConfig: OverridableConfig{
			Interval: intPtr(15),
		},
	}

	resolved := cfg.ResolveConfigForRoom(room, "douyin")
	
	// Room-level override should take precedence
	assert.Equal(t, 15, resolved.Interval)
	// Platform-level override should take precedence over global
	assert.Equal(t, "/douyin", resolved.OutPutPath)
	// Global value should be used when no override exists
	assert.Equal(t, "/usr/bin/ffmpeg", resolved.FfmpegPath)
}

func TestGetPlatformMinAccessInterval(t *testing.T) {
	cfg := &Config{
		PlatformConfigs: map[string]PlatformConfig{
			"douyin": {
				OverridableConfig:    OverridableConfig{},
				MinAccessIntervalSec: 5,
			},
		},
	}

	// Test existing platform
	interval := cfg.GetPlatformMinAccessInterval("douyin")
	assert.Equal(t, 5, interval)

	// Test non-existing platform
	interval = cfg.GetPlatformMinAccessInterval("bilibili")
	assert.Equal(t, 0, interval)
}

func TestBackwardsCompatibility(t *testing.T) {
	// Test that old config files still work
	oldConfigYaml := `
rpc:
  enable: true
  bind: :8080
debug: false
interval: 30
out_put_path: ./
live_rooms:
- url: https://live.bilibili.com/123456
  is_listening: true
`
	cfg, err := NewConfigWithBytes([]byte(oldConfigYaml))
	assert.NoError(t, err)
	assert.NotNil(t, cfg.PlatformConfigs)
	assert.Equal(t, 30, cfg.Interval)
	assert.Len(t, cfg.LiveRooms, 1)
	assert.Equal(t, "https://live.bilibili.com/123456", cfg.LiveRooms[0].Url)
	
	// Test that resolve works with no overrides
	resolved := cfg.ResolveConfigForRoom(&cfg.LiveRooms[0], "bilibili")
	assert.Equal(t, 30, resolved.Interval)
	assert.Equal(t, "./", resolved.OutPutPath)
}

func TestGetPlatformKeyFromUrl(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://live.bilibili.com/123456", "bilibili"},
		{"https://live.douyin.com/789", "douyin"},
		{"https://v.douyin.com/abc", "douyin"},
		{"https://www.douyu.com/room/123", "douyu"},
		{"https://unknown.domain.com/room", "unknown.domain.com"},
		{"invalid-url", ""},
	}

	for _, test := range tests {
		result := GetPlatformKeyFromUrl(test.url)
		assert.Equal(t, test.expected, result, "URL: %s", test.url)
	}
}

func TestHierarchicalConfigExample(t *testing.T) {
	// Test that our example hierarchical config can be parsed
	cfg, err := NewConfigWithFile("../../config.hierarchical.example.yml")
	assert.NoError(t, err)
	
	// Test global settings
	assert.Equal(t, 30, cfg.Interval)
	assert.Equal(t, "./recordings", cfg.OutPutPath)
	
	// Test platform configs
	assert.Len(t, cfg.PlatformConfigs, 3)
	
	// Test Douyin platform config
	douyinConfig := cfg.PlatformConfigs["douyin"]
	assert.Equal(t, "抖音", douyinConfig.Name)
	assert.Equal(t, 5, douyinConfig.MinAccessIntervalSec)
	assert.NotNil(t, douyinConfig.Interval)
	assert.Equal(t, 15, *douyinConfig.Interval)
	assert.NotNil(t, douyinConfig.OutPutPath)
	assert.Equal(t, "./recordings/douyin", *douyinConfig.OutPutPath)
	
	// Test Bilibili platform config
	bilibiliConfig := cfg.PlatformConfigs["bilibili"] 
	assert.Equal(t, "哔哩哔哩", bilibiliConfig.Name)
	assert.Equal(t, 3, bilibiliConfig.MinAccessIntervalSec)
	
	// Test live rooms
	assert.Len(t, cfg.LiveRooms, 4)
	
	// Test resolution for Douyin room with override
	douyinRoom := cfg.LiveRooms[0]
	resolvedDouyin := cfg.ResolveConfigForRoom(&douyinRoom, "douyin")
	assert.Equal(t, 10, resolvedDouyin.Interval)  // Room-level override
	assert.Equal(t, "./recordings/douyin", resolvedDouyin.OutPutPath)  // Platform-level override
	
	// Test resolution for standard Bilibili room
	bilibiliRoom := cfg.LiveRooms[1]
	resolvedBilibili := cfg.ResolveConfigForRoom(&bilibiliRoom, "bilibili")
	assert.Equal(t, 20, resolvedBilibili.Interval)  // Platform-level override
	assert.Equal(t, "./recordings", resolvedBilibili.OutPutPath)  // Global setting
	assert.True(t, resolvedBilibili.Feature.UseNativeFlvParser)  // Platform-level override
	
	// Test resolution for Bilibili room with room-level overrides
	specialBilibiliRoom := cfg.LiveRooms[2]
	resolvedSpecial := cfg.ResolveConfigForRoom(&specialBilibiliRoom, "bilibili")
	assert.Equal(t, 20, resolvedSpecial.Interval)  // Platform-level override
	assert.Equal(t, "./recordings/special", resolvedSpecial.OutPutPath)  // Room-level override
	assert.Equal(t, "/opt/ffmpeg/bin/ffmpeg", resolvedSpecial.FfmpegPath)  // Room-level override
}

// Helper functions for pointer conversion
func intPtr(i int) *int {
	return &i
}

func stringPtr(s string) *string {
	return &s
}
