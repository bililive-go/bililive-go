package configs

// DownloaderType 表示下载器类型
type DownloaderType string

const (
	// DownloaderFFmpeg 使用 ffmpeg 进行下载
	DownloaderFFmpeg DownloaderType = "ffmpeg"
	// DownloaderNative 使用内置的原生 FLV 解析器
	DownloaderNative DownloaderType = "native"
	// DownloaderBililiveRecorder 使用 BililiveRecorder CLI 进行下载
	DownloaderBililiveRecorder DownloaderType = "bililive-recorder"
)

// AllDownloaderTypes 返回所有可用的下载器类型
func AllDownloaderTypes() []DownloaderType {
	return []DownloaderType{
		DownloaderFFmpeg,
		DownloaderNative,
		DownloaderBililiveRecorder,
	}
}

// IsValid 检查下载器类型是否有效
func (d DownloaderType) IsValid() bool {
	switch d {
	case DownloaderFFmpeg, DownloaderNative, DownloaderBililiveRecorder:
		return true
	default:
		return false
	}
}

// String 返回下载器类型的字符串表示
func (d DownloaderType) String() string {
	return string(d)
}

// DisplayName 返回下载器类型的显示名称
func (d DownloaderType) DisplayName() string {
	switch d {
	case DownloaderFFmpeg:
		return "FFmpeg"
	case DownloaderNative:
		return "原生 FLV 解析器"
	case DownloaderBililiveRecorder:
		return "BililiveRecorder"
	default:
		return string(d)
	}
}

// ParseDownloaderType 将字符串解析为 DownloaderType
func ParseDownloaderType(s string) DownloaderType {
	switch s {
	case "ffmpeg", "":
		return DownloaderFFmpeg
	case "native":
		return DownloaderNative
	case "bililive-recorder":
		return DownloaderBililiveRecorder
	default:
		return DownloaderFFmpeg
	}
}
