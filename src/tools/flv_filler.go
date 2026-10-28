package tools

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
)

// fillerEvidence 只为单文件、有效媒体逐包一致的 H.264 填充删除放宽体积检查。
type fillerEvidence struct {
	Method       string `json:"method"`
	RemovedBytes int64  `json:"removed_filler_bytes"` // 包含 NAL 长度前缀
	RemovedUnits int64  `json:"removed_filler_units"`
}

type fillerFingerprint struct {
	size                         int64
	flags                        byte
	audio, video                 [32]byte
	audioTags, videoTags, frames int64
	fillerBytes, fillerUnits     int64
}

// 两种体积路径共用严格的原片扫描与填充比较，输出指纹复用已有扫描结果。
func verifyFillerRemoval(ctx context.Context, input string, output fillerFingerprint) (fillerEvidence, error) {
	_, source, err := inspectWithoutFiller(ctx, input)
	if err != nil {
		return fillerEvidence{}, err
	}
	return compareFillerRemoval(source, output)
}

func compareFillerRemoval(source, output fillerFingerprint) (fillerEvidence, error) {
	var evidence fillerEvidence
	// 时间戳、包边界、编码参数和所有非填充数据都必须完全相同；不使用百分比容差。
	if source.flags != output.flags || source.audio != output.audio || source.video != output.video || source.audioTags != output.audioTags || source.videoTags != output.videoTags || source.frames != output.frames {
		return evidence, fmt.Errorf("audio/video data, codec configuration, packet boundaries or timestamps changed")
	}
	if source.fillerUnits <= output.fillerUnits || source.fillerBytes <= output.fillerBytes {
		return evidence, fmt.Errorf("no verified H.264 filler reduction")
	}
	evidence = fillerEvidence{Method: "h264_filler_only", RemovedBytes: source.fillerBytes - output.fillerBytes, RemovedUnits: source.fillerUnits - output.fillerUnits}
	// 允许重建索引使元数据增大，但不能用少量填充删除掩盖其他区域的大幅缩小。
	if source.size-output.size > evidence.RemovedBytes {
		return fillerEvidence{}, fmt.Errorf("size reduction exceeds verified filler removal")
	}
	return evidence, nil
}

func hashUint32(h hash.Hash, n uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], n)
	_, _ = h.Write(b[:])
}
func hashMediaTag(h hash.Hash, timestamp uint32, payload []byte) {
	hashUint32(h, timestamp)
	hashUint32(h, uint32(len(payload)))
	_, _ = h.Write(payload)
}

// 每次只缓存一个标签（FLV 标签长度最多 16 MiB），不把整个录像读入内存。
// 元数据允许由修复工具重建；音视频编码头、结束标记和媒体时间戳必须逐包保留。
func inspectWithoutFiller(ctx context.Context, path string) (FLVStats, fillerFingerprint, error) {
	var result fillerFingerprint
	audio, video := sha256.New(), sha256.New()
	nalLength := 0
	stats, err := inspectFLV(ctx, path, func(tag, payload []byte) error {
		n := len(payload)
		timestamp := uint32(tag[7])<<24 | uint32(tag[4])<<16 | uint32(tag[5])<<8 | uint32(tag[6])
		var err error
		switch tag[0] {
		case 18: // onMetaData 的索引等封装信息不属于编码后的音视频内容。
		case 8:
			if n == 0 {
				return fmt.Errorf("empty audio tag")
			}
			hashMediaTag(audio, timestamp, payload)
			result.audioTags++
		case 9:
			if n < 5 || payload[0]&15 != 7 || payload[0]>>4 < 1 || payload[0]>>4 > 4 {
				return fmt.Errorf("only classic H.264/AVC video is supported")
			}
			result.videoTags++
			switch payload[1] {
			case 0:
				nalLength, err = avcNALLength(payload[5:])
				if err != nil {
					return err
				}
				hashMediaTag(video, timestamp, payload)
			case 1:
				if nalLength == 0 {
					return fmt.Errorf("AVC packet before decoder configuration")
				}
				kept, err := visitAVCNALs(payload[5:], nalLength, func(nal []byte) error {
					if nal[0]&31 == 12 {
						if !validH264Filler(nal) {
							return fmt.Errorf("invalid H.264 filler RBSP")
						}
						result.fillerUnits++
						result.fillerBytes += int64(len(nal) + nalLength)
					}
					return nil
				})
				if err != nil {
					return err
				}
				// 保留每个视频包边界及 DTS/CTS；填充以外的 NAL 长度和顺序也纳入哈希。
				hashUint32(video, timestamp)
				_, _ = video.Write(payload[:5])
				hashUint32(video, uint32(kept))
				_, err = visitAVCNALs(payload[5:], nalLength, func(nal []byte) error {
					if nal[0]&31 != 12 {
						hashUint32(video, uint32(len(nal)))
						_, _ = video.Write(nal)
					}
					return nil
				})
				if err != nil {
					return err
				}
				result.frames++
			case 2:
				if n != 5 {
					return fmt.Errorf("invalid AVC end marker")
				}
				hashMediaTag(video, timestamp, payload)
			default:
				return fmt.Errorf("unsupported AVC packet type")
			}
		default:
			return fmt.Errorf("unsupported FLV tag: %d", tag[0])
		}
		return nil
	})
	if err != nil {
		return stats, result, err
	}
	result.flags = stats.flags
	result.size = stats.Size

	if result.frames == 0 {
		return stats, result, fmt.Errorf("no configured H.264 media packets")
	}
	copy(result.audio[:], audio.Sum(nil))
	copy(result.video[:], video.Sum(nil))
	return stats, result, nil
}

func avcNALLength(config []byte) (int, error) {
	if len(config) < 7 || config[0] != 1 || config[4]&0xfc != 0xfc || config[5]&0xe0 != 0xe0 {
		return 0, fmt.Errorf("invalid AVC decoder configuration")
	}
	n := int(config[4]&3) + 1
	if n == 3 {
		return 0, fmt.Errorf("reserved AVC NAL length size")
	}
	// 检查 SPS/PPS 数组边界；其余高 profile 扩展仍由完整配置哈希保护。
	pos := 6
	sps := int(config[5] & 31)
	if sps == 0 {
		return 0, fmt.Errorf("missing SPS")
	}
	for group := 0; group < 2; group++ {
		count := sps
		if group == 1 {
			if pos >= len(config) {
				return 0, fmt.Errorf("missing PPS count")
			}
			count = int(config[pos])
			pos++
			if count == 0 {
				return 0, fmt.Errorf("missing PPS")
			}
		}
		for i := 0; i < count; i++ {
			if pos+2 > len(config) {
				return 0, fmt.Errorf("truncated AVC parameter length")
			}
			size := int(binary.BigEndian.Uint16(config[pos : pos+2]))
			pos += 2
			if size == 0 || pos+size > len(config) {
				return 0, fmt.Errorf("truncated AVC parameter")
			}
			pos += size
		}
	}
	return n, nil
}

func visitAVCNALs(data []byte, nalLength int, visit func([]byte) error) (int, error) {
	kept := 0
	for pos := 0; pos < len(data); {
		if pos+nalLength > len(data) {
			return 0, fmt.Errorf("truncated AVC NAL length")
		}
		var size uint32
		for _, b := range data[pos : pos+nalLength] {
			size = size<<8 | uint32(b)
		}
		pos += nalLength
		if size == 0 || uint64(size) > uint64(len(data)-pos) {
			return 0, fmt.Errorf("invalid AVC NAL size")
		}
		nal := data[pos : pos+int(size)]
		pos += int(size)
		if nal[0]&0x80 != 0 || nal[0]&31 == 0 || nal[0]&31 >= 24 {
			return 0, fmt.Errorf("unsupported or invalid AVC NAL header")
		}
		if err := visit(nal); err != nil {
			return 0, err
		}
		if nal[0]&31 != 12 {
			kept++
		}
	}
	if len(data) == 0 {
		return 0, fmt.Errorf("empty AVC packet")
	}
	return kept, nil
}

func validH264Filler(nal []byte) bool {
	if len(nal) < 2 || nal[0] != 12 || nal[len(nal)-1] != 0x80 {
		return false
	}
	for _, b := range nal[1 : len(nal)-1] {
		if b != 0xff {
			return false
		}
	}
	return true
}
