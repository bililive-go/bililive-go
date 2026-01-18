package live

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// StreamSelector 流选择器
type StreamSelector struct {
	preference StreamPreference
}

// NewStreamSelector 创建流选择器
func NewStreamSelector(preference StreamPreference) *StreamSelector {
	return &StreamSelector{
		preference: preference,
	}
}

// SelectBestStreamFromUrlInfos 从StreamUrlInfo切片中选择最佳流
func (s *StreamSelector) SelectBestStreamFromUrlInfos(urlInfos []*StreamUrlInfo) (*StreamUrlInfo, string) {
	if len(urlInfos) == 0 {
		return nil, "无可用流"
	}

	// 只有一个流时直接返回
	if len(urlInfos) == 1 {
		return urlInfos[0], "唯一可用流"
	}

	// 构建评分系统
	type scoredStream struct {
		stream  *StreamUrlInfo
		score   int
		reasons []string
	}

	scored := make([]scoredStream, 0, len(urlInfos))

	for _, urlInfo := range urlInfos {
		score := 0
		reasons := []string{}

		// 1. 格式匹配 (优先级最高，100分为基准)
		formatIndex := -1
		for i, preferredFormat := range s.preference.Formats {
			if urlInfo.Format == preferredFormat {
				formatIndex = i
				break
			}
		}
		if formatIndex >= 0 {
			// 第一个格式100分，第二个90分，依此类推
			formatScore := 100 - (formatIndex * 10)
			score += formatScore
			reasons = append(reasons, fmt.Sprintf("格式优先级#%d(+%d)", formatIndex+1, formatScore))
		} else if urlInfo.Format != "" {
			reasons = append(reasons, "格式不匹配")
		}

		// 2. 分辨率匹配 (50分为基准)
		qualityIndex := -1
		normalizedQuality := NormalizeQuality(urlInfo.Quality)
		for i, preferredQuality := range s.preference.Qualities {
			if normalizedQuality == NormalizeQuality(preferredQuality) {
				qualityIndex = i
				break
			}
		}
		if qualityIndex >= 0 {
			qualityScore := 50 - (qualityIndex * 5)
			score += qualityScore
			reasons = append(reasons, fmt.Sprintf("分辨率优先级#%d(+%d)", qualityIndex+1, qualityScore))
		} else {
			// 没有完全匹配，计算最接近的
			if len(s.preference.Qualities) > 0 && urlInfo.Width > 0 {
				targetW, targetH := ParseResolution(s.preference.Qualities[0])
				if targetW > 0 {
					targetPixels := targetW * targetH
					actualPixels := urlInfo.Width * urlInfo.Height
					diff := int(math.Abs(float64(actualPixels - targetPixels)))
					// 差异越小，分数越高（最多25分）
					closenessScore := 25 - (diff / 100000)
					if closenessScore < 0 {
						closenessScore = 0
					}
					if closenessScore > 25 {
						closenessScore = 25
					}
					score += closenessScore
					if closenessScore > 0 {
						reasons = append(reasons, fmt.Sprintf("分辨率接近(+%d)", closenessScore))
					}
				}
			}
		}

		// 3. 码率检查
		if s.preference.MaxBitrate > 0 && urlInfo.Bitrate > s.preference.MaxBitrate {
			score -= 20
			reasons = append(reasons, fmt.Sprintf("码率超限(-20) %dkbps>%dkbps", urlInfo.Bitrate, s.preference.MaxBitrate))
		}
		if s.preference.MinBitrate > 0 && urlInfo.Bitrate > 0 && urlInfo.Bitrate < s.preference.MinBitrate {
			score -= 20
			reasons = append(reasons, fmt.Sprintf("码率过低(-20) %dkbps<%dkbps", urlInfo.Bitrate, s.preference.MinBitrate))
		}

		// 4. H.265检查
		if !s.preference.AllowH265 && urlInfo.Codec == "h265" {
			score -= 30
			reasons = append(reasons, "不允许H.265(-30)")
		}

		// 5. 60fps偏好
		if s.preference.Prefer60FPS && urlInfo.FrameRate >= 59 && urlInfo.FrameRate <= 61 {
			score += 10
			reasons = append(reasons, "60fps(+10)")
		}

		scored = append(scored, scoredStream{
			stream:  urlInfo,
			score:   score,
			reasons: reasons,
		})
	}

	// 排序：分数越高越好
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	if len(scored) > 0 {
		best := scored[0]
		reason := strings.Join(best.reasons, ", ")
		if reason == "" {
			reason = "默认选择"
		}
		return best.stream, fmt.Sprintf("得分%d: %s", best.score, reason)
	}

	return urlInfos[0], "Fallback"
}
