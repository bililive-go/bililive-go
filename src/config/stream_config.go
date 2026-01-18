package config

import (
	"github.com/bililive-go/bililive-go/src/live"
)

// StreamConfig 直播流配置（三级覆盖）
type StreamConfig struct {
	// 全局默认配置
	Global live.StreamPreference `yaml:"global" json:"global"`

	// 平台级配置（覆盖全局）
	// key为平台名: "bilibili", "douyu", "huya"
	Platforms map[string]live.StreamPreference `yaml:"platforms" json:"platforms"`

	// 房间级配置（最高优先级）
	// key为 "platform_roomid": "bilibili_12345"
	Rooms map[string]live.StreamPreference `yaml:"rooms" json:"rooms"`
}

// DefaultStreamConfig 默认流配置
func DefaultStreamConfig() *StreamConfig {
	return &StreamConfig{
		Global:    live.DefaultStreamPreference,
		Platforms: make(map[string]live.StreamPreference),
		Rooms:     make(map[string]live.StreamPreference),
	}
}

// GetPreference 获取指定房间的流偏好（应用三级覆盖）
func (sc *StreamConfig) GetPreference(platform, roomID string) live.StreamPreference {
	// 从全局配置开始
	pref := sc.Global

	// 应用平台级配置
	if platformPref, ok := sc.Platforms[platform]; ok {
		pref = mergePreference(pref, platformPref)
	}

	// 应用房间级配置
	roomKey := platform + "_" + roomID
	if roomPref, ok := sc.Rooms[roomKey]; ok {
		pref = mergePreference(pref, roomPref)
	}

	return pref
}

// SetGlobalPreference 设置全局偏好
func (sc *StreamConfig) SetGlobalPreference(pref live.StreamPreference) {
	sc.Global = pref
}

// SetPlatformPreference 设置平台级偏好
func (sc *StreamConfig) SetPlatformPreference(platform string, pref live.StreamPreference) {
	if sc.Platforms == nil {
		sc.Platforms = make(map[string]live.StreamPreference)
	}
	sc.Platforms[platform] = pref
}

// SetRoomPreference 设置房间级偏好
func (sc *StreamConfig) SetRoomPreference(platform, roomID string, pref live.StreamPreference) {
	if sc.Rooms == nil {
		sc.Rooms = make(map[string]live.StreamPreference)
	}
	roomKey := platform + "_" + roomID
	sc.Rooms[roomKey] = pref
}

// DeletePlatformPreference 删除平台级配置
func (sc *StreamConfig) DeletePlatformPreference(platform string) {
	delete(sc.Platforms, platform)
}

// DeleteRoomPreference 删除房间级配置
func (sc *StreamConfig) DeleteRoomPreference(platform, roomID string) {
	roomKey := platform + "_" + roomID
	delete(sc.Rooms, roomKey)
}

// mergePreference 合并偏好设置（child覆盖parent）
// 只有child中非零值的字段才会覆盖parent
func mergePreference(parent, child live.StreamPreference) live.StreamPreference {
	merged := parent

	// 合并Formats
	if len(child.Formats) > 0 {
		merged.Formats = child.Formats
	}

	// 合并Qualities
	if len(child.Qualities) > 0 {
		merged.Qualities = child.Qualities
	}

	// 合并MaxBitrate（0表示不限制，也是有效值）
	if child.MaxBitrate != 0 || len(child.Formats) > 0 {
		// 如果child有任何配置，就使用child的MaxBitrate
		merged.MaxBitrate = child.MaxBitrate
	}

	// 合并MinBitrate
	if child.MinBitrate != 0 || len(child.Formats) > 0 {
		merged.MinBitrate = child.MinBitrate
	}

	// 合并AllowH265
	// 由于bool的零值是false，我们需要检查是否是显式设置
	// 这里简化处理：如果child有任何配置，就使用child的值
	if len(child.Formats) > 0 || len(child.Qualities) > 0 {
		merged.AllowH265 = child.AllowH265
	}

	// 合并Prefer60FPS
	if len(child.Formats) > 0 || len(child.Qualities) > 0 {
		merged.Prefer60FPS = child.Prefer60FPS
	}

	return merged
}

// ValidatePreference 验证偏好配置的有效性
func ValidatePreference(pref *live.StreamPreference) []string {
	errors := []string{}

	// 验证格式
	validFormats := map[string]bool{
		"flv":  true,
		"hls":  true,
		"rtmp": true,
	}

	for _, format := range pref.Formats {
		if !validFormats[format] {
			errors = append(errors, "无效的格式: "+format)
		}
	}

	// 验证分辨率
	validQualities := map[string]bool{
		"4k":       true,
		"original": true,
		"1080p":    true,
		"720p":     true,
		"480p":     true,
		"360p":     true,
		"原画":       true,
		"蓝光":       true,
		"超清":       true,
		"高清":       true,
		"流畅":       true,
	}

	for _, quality := range pref.Qualities {
		normalized := live.NormalizeQuality(quality)
		// 允许自定义质量标识，所以不对未知的标识报错
		_ = normalized
		_ = validQualities
	}

	// 验证码率
	if pref.MaxBitrate < 0 {
		errors = append(errors, "最大码率不能为负数")
	}

	if pref.MinBitrate < 0 {
		errors = append(errors, "最小码率不能为负数")
	}

	if pref.MinBitrate > 0 && pref.MaxBitrate > 0 && pref.MinBitrate > pref.MaxBitrate {
		errors = append(errors, "最小码率不能大于最大码率")
	}

	return errors
}

// 扩展Config结构（添加到现有config.go）
type Config struct {
	// ... 现有字段 ...

	// 新增：流配置
	Stream StreamConfig `yaml:"stream" json:"stream"`
}

// InitStreamConfig 初始化流配置（如果为空）
func (c *Config) InitStreamConfig() {
	if c.Stream.Platforms == nil {
		c.Stream.Platforms = make(map[string]live.StreamPreference)
	}

	if c.Stream.Rooms == nil {
		c.Stream.Rooms = make(map[string]live.StreamPreference)
	}

	// 如果全局配置为空，使用默认值
	if len(c.Stream.Global.Formats) == 0 {
		c.Stream.Global = live.DefaultStreamPreference
	}
}
