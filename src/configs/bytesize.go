package configs

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ByteSize 表示文件大小，支持可读格式（如 "1GB"、"500MB"）。
// 内部以字节为单位存储。
//
// 支持的格式：
//   - 纯数字：视为字节（向后兼容），如 1073741824
//   - 带单位：如 "500MB"、"1GB"、"1.5gb"、"500 MB"
//   - 支持的单位：B, KB, MB, GB, TB（不区分大小写）
//   - 0 表示不限制
type ByteSize int64

const (
	B  ByteSize = 1
	KB ByteSize = 1024
	MB ByteSize = 1024 * 1024
	GB ByteSize = 1024 * 1024 * 1024
	TB ByteSize = 1024 * 1024 * 1024 * 1024
)

// ParseByteSize 将可读的文件大小字符串解析为字节数。
func ParseByteSize(s string) (ByteSize, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}

	// 尝试直接解析为纯数字（向后兼容）
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return ByteSize(n), nil
	}

	// 分离数字和单位
	s = strings.TrimSpace(s)
	upper := strings.ToUpper(s)

	// 按单位从长到短匹配，避免 "B" 先匹配到 "TB" 中的 B
	units := []struct {
		suffix     string
		multiplier ByteSize
	}{
		{"TB", TB},
		{"GB", GB},
		{"MB", MB},
		{"KB", KB},
		{"B", B},
	}

	for _, u := range units {
		if strings.HasSuffix(upper, u.suffix) {
			numStr := strings.TrimSpace(s[:len(s)-len(u.suffix)])
			if numStr == "" {
				return 0, fmt.Errorf("无效的文件大小格式: %q", s)
			}
			val, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0, fmt.Errorf("无效的文件大小格式: %q", s)
			}
			if val < 0 {
				return ByteSize(val), nil // 保留负数，由调用方校验
			}
			result := val * float64(u.multiplier)
			if result > math.MaxInt64 {
				return 0, fmt.Errorf("文件大小超出范围: %q", s)
			}
			return ByteSize(int64(result)), nil
		}
	}

	return 0, fmt.Errorf("无效的文件大小格式: %q，支持的单位: B, KB, MB, GB, TB", s)
}

// Bytes 返回字节数。
func (b ByteSize) Bytes() int64 {
	return int64(b)
}

// String 返回可读的格式。
// 0 返回 "0"，能整除的用整数表示，否则保留两位小数。
func (b ByteSize) String() string {
	if b == 0 {
		return "0"
	}

	abs := b
	prefix := ""
	if b < 0 {
		abs = -b
		prefix = "-"
	}

	switch {
	case abs >= TB && abs%TB == 0:
		return prefix + strconv.FormatInt(int64(abs/TB), 10) + "TB"
	case abs >= GB && abs%GB == 0:
		return prefix + strconv.FormatInt(int64(abs/GB), 10) + "GB"
	case abs >= MB && abs%MB == 0:
		return prefix + strconv.FormatInt(int64(abs/MB), 10) + "MB"
	case abs >= KB && abs%KB == 0:
		return prefix + strconv.FormatInt(int64(abs/KB), 10) + "KB"
	case abs >= TB:
		return prefix + strconv.FormatFloat(float64(abs)/float64(TB), 'f', 2, 64) + "TB"
	case abs >= GB:
		return prefix + strconv.FormatFloat(float64(abs)/float64(GB), 'f', 2, 64) + "GB"
	case abs >= MB:
		return prefix + strconv.FormatFloat(float64(abs)/float64(MB), 'f', 2, 64) + "MB"
	case abs >= KB:
		return prefix + strconv.FormatFloat(float64(abs)/float64(KB), 'f', 2, 64) + "KB"
	default:
		return prefix + strconv.FormatInt(int64(abs), 10)
	}
}

// UnmarshalYAML 实现 yaml.Unmarshaler 接口。
// 支持数字（向后兼容）和字符串格式。
func (b *ByteSize) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// 先尝试解析为整数（向后兼容旧配置）
	var n int64
	if err := unmarshal(&n); err == nil {
		*b = ByteSize(n)
		return nil
	}

	// 再尝试解析为浮点数
	var f float64
	if err := unmarshal(&f); err == nil {
		*b = ByteSize(int64(f))
		return nil
	}

	// 最后尝试解析为字符串
	var s string
	if err := unmarshal(&s); err != nil {
		return fmt.Errorf("无法解析文件大小: 需要数字或带单位的字符串 (如 500MB, 1GB)")
	}

	parsed, err := ParseByteSize(s)
	if err != nil {
		return err
	}
	*b = parsed
	return nil
}

// MarshalYAML 实现 yaml.Marshaler 接口。
// 输出可读格式。
func (b ByteSize) MarshalYAML() (interface{}, error) {
	return b.String(), nil
}

// UnmarshalJSON 实现 json.Unmarshaler 接口。
// 支持数字（向后兼容）和字符串格式。
func (b *ByteSize) UnmarshalJSON(data []byte) error {
	// 先尝试解析为数字
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		*b = ByteSize(n)
		return nil
	}

	// 再尝试解析为字符串
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("无法解析文件大小: 需要数字或带单位的字符串 (如 500MB, 1GB)")
	}

	parsed, err := ParseByteSize(s)
	if err != nil {
		return err
	}
	*b = parsed
	return nil
}

// MarshalJSON 实现 json.Marshaler 接口。
// 输出可读的字符串格式。
func (b ByteSize) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.String())
}
