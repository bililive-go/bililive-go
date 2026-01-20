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

	// Test non-existing platform - returns default minimum interval of 1 second
	interval = cfg.GetPlatformMinAccessInterval("bilibili")
	assert.Equal(t, 1, interval) // 默认最小间隔为 1 秒，防止无限制高频访问
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

func TestHierarchicalConfigFromExistingConfig(t *testing.T) {
	// 使用内联配置字符串测试层级配置功能，不依赖外部 config.yml 文件
	hierarchicalConfigYaml := `
rpc:
  enable: true
  bind: :8080
debug: false
interval: 20
out_put_path: ./
live_rooms:
- url: https://live.bilibili.com/123456
  is_listening: true
platform_configs:
  bilibili:
    interval: 30
    name: "哔哩哔哩"
    min_access_interval_sec: 1
  douyin:
    interval: 15
    name: "抖音"
`
	cfg, err := NewConfigWithBytes([]byte(hierarchicalConfigYaml))
	assert.NoError(t, err)
	assert.NotNil(t, cfg.PlatformConfigs)
	assert.Equal(t, 20, cfg.Interval) // 全局配置
	assert.Equal(t, "./", cfg.OutPutPath)

	// 验证平台配置已正确加载
	assert.Len(t, cfg.PlatformConfigs, 2)
	assert.Equal(t, 30, *cfg.PlatformConfigs["bilibili"].Interval)
	assert.Equal(t, 15, *cfg.PlatformConfigs["douyin"].Interval)

	// 测试 bilibili 平台使用平台级覆盖配置
	room := &LiveRoom{Url: "https://live.bilibili.com/123456"}
	resolved := cfg.ResolveConfigForRoom(room, "bilibili")
	assert.Equal(t, 30, resolved.Interval)     // 平台级覆盖 (bilibili 有 interval: 30)
	assert.Equal(t, "./", resolved.OutPutPath) // 使用全局设置 (无覆盖)

	// 测试 douyin 平台使用平台级覆盖配置
	roomDouyin := &LiveRoom{Url: "https://live.douyin.com/789"}
	resolvedDouyin := cfg.ResolveConfigForRoom(roomDouyin, "douyin")
	assert.Equal(t, 15, resolvedDouyin.Interval) // 平台级覆盖 (douyin 有 interval: 15)

	// 测试没有平台配置时使用全局默认值
	roomUnknown := &LiveRoom{Url: "https://unknown.platform.com/123"}
	resolvedUnknown := cfg.ResolveConfigForRoom(roomUnknown, "unknown")
	assert.Equal(t, 20, resolvedUnknown.Interval) // 使用全局默认值
}

// Helper functions for pointer conversion
func intPtr(i int) *int {
	return &i
}

func stringPtr(s string) *string {
	return &s
}
