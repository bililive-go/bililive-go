package flv

import (
	"context"
	"errors"
)

type (
	FrameType     uint8
	CodeID        uint8
	AVCPacketType uint8

	VideoTagHeader struct {
		FrameType       FrameType
		CodeID          CodeID
		AVCPacketType   AVCPacketType
		CompositionTime uint32
	}
)

const (
	// Frame Type
	KeyFrame             FrameType = 1 // for AVC, a seekable frame
	InterFrame           FrameType = 2 // for AVC, a non-seekable frame
	DisposableInterFrame FrameType = 3 // H.263 only
	GeneratedKeyFrame    FrameType = 4 // reserved for server use only
	VideoInfoFrame       FrameType = 5 // video info/command frame

	// CodeID
	H263Code          CodeID = 2 // Sorenson H.263
	ScreenVideoCode   CodeID = 3 // Screen video
	VP6Code           CodeID = 4 // On2 VP6
	VP6AlphaCode      CodeID = 5 // On2 VP6 with alpha channel
	ScreenVideoV2Code CodeID = 6 // Screen video version 2
	AVCCode           CodeID = 7 // AVC

	// AVCPacketType
	AVCSeqHeader AVCPacketType = 0 // AVC sequence header
	AVCNALU      AVCPacketType = 1 // NALU
	AVCEndSeq    AVCPacketType = 2 // AVC end of sequence (lower level NALU sequence ender is not required or supported)
)

func (p *Parser) parseVideoTag(ctx context.Context, length, timestamp uint32) (*VideoTagHeader, error) {
	// header
	b, err := p.i.ReadByte()
	l := length - 1
	if err != nil {
		return nil, err
	}
	tag := new(VideoTagHeader)
	tag.FrameType = FrameType(b >> 4 & 15)
	tag.CodeID = CodeID(b & 15)

	if tag.CodeID == AVCCode {
		// read AVCPacketType
		b, err := p.i.ReadByte()
		l -= 1
		if err != nil {
			return nil, err
		}
		tag.AVCPacketType = AVCPacketType(b)
		switch tag.AVCPacketType {
		case AVCNALU:
			// read CompositionTime
			b, err := p.i.ReadN(3)
			l -= 3
			if err != nil {
				return nil, err
			}
			tag.CompositionTime = uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
		case AVCSeqHeader:
			p.avcHeaderCount++
			if p.avcHeaderCount > 1 {
				// new sps pps
				return nil, errors.New("EOF new sps pps")
			}
		}
	}

	// write tag header && video tag header & AVCPacketType & CompositionTime
	if err := p.doWrite(ctx, p.i.AllBytes()); err != nil {
		return nil, err
	}
	p.i.Reset()
	// write body
	if err := p.doCopy(ctx, l); err != nil {
		return nil, err
	}

	return tag, nil
}

// skipVideoTag 跳过视频标签数据（用于 audio_only 模式）
// 读取并丢弃数据，不写入输出文件
func (p *Parser) skipVideoTag(ctx context.Context, length uint32) (*VideoTagHeader, error) {
	// 读取但不写入 tag header 数据
	p.i.Reset()

	// 跳过整个 tag body
	if _, err := p.i.ReadN(int(length)); err != nil {
		return nil, err
	}
	p.i.Reset()

	return nil, nil
}
