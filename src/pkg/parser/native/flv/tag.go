package flv

import "context"

func (p *Parser) parseTag(ctx context.Context) error {
	p.tagCount += 1

	b, err := p.i.ReadN(15)
	if err != nil {
		return err
	}

	tagType := uint8(b[4])
	length := uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7])
	timestamp := uint32(b[8])<<16 | uint32(b[9])<<8 | uint32(b[10]) | uint32(b[11])<<24

	switch tagType {
	case audioTag:
		if _, err := p.parseAudioTag(ctx, length, timestamp); err != nil {
			return err
		}
	case videoTag:
		// 只录音频模式：跳过视频标签（仍需读取数据但不写入）
		if p.audioOnly {
			if _, err := p.skipVideoTag(ctx, length); err != nil {
				return err
			}
		} else {
			if _, err := p.parseVideoTag(ctx, length, timestamp); err != nil {
				return err
			}
		}
	case scriptTag:
		return p.parseScriptTag(ctx, length)
	default:
		return ErrUnknownTag
	}

	return nil
}
