package bilibili

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/andybalholm/brotli"
)

// 协议版本
const (
	ProtoPlain      uint16 = 0 // 无压缩，单个 JSON
	ProtoPopularity uint16 = 1 // 心跳回复（人气值）
	ProtoZlib       uint16 = 2 // zlib 压缩，可能包含多个子包
	ProtoBrotli     uint16 = 3 // brotli 压缩，可能包含多个子包
)

// 操作码
const (
	OpHeartBeat      uint32 = 2 // 客户端发送心跳
	OpHeartBeatReply uint32 = 3 // 服务端心跳回复（人气值）
	OpNotification   uint32 = 5 // 业务消息（弹幕/礼物等）
	OpAuth           uint32 = 7 // 客户端发送认证
	OpAuthReply      uint32 = 8 // 服务端认证回复
)

const headerLen = 16

// Packet 表示一个 B站弹幕协议数据包
type Packet struct {
	ProtocolVersion uint16
	Operation       uint32
	Body            []byte
}

// Build 构建数据包字节序列
func (p Packet) Build() []byte {
	buf := make([]byte, headerLen)
	binary.BigEndian.PutUint16(buf[4:6], headerLen) // HeaderLength = 16
	binary.BigEndian.PutUint16(buf[6:8], p.ProtocolVersion)
	binary.BigEndian.PutUint32(buf[8:12], p.Operation)
	binary.BigEndian.PutUint32(buf[12:16], 1) // SequenceID
	buf = append(buf, p.Body...)
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(buf)))
	return buf
}

// ParsePacket 从字节流解析单个数据包
func ParsePacket(data []byte) (Packet, error) {
	if len(data) < headerLen {
		return Packet{}, fmt.Errorf("packet too short: %d bytes", len(data))
	}
	packLen := binary.BigEndian.Uint32(data[0:4])
	if uint64(packLen) > uint64(len(data)) {
		return Packet{}, fmt.Errorf("packet length %d exceeds data %d", packLen, len(data))
	}
	if packLen < headerLen {
		return Packet{}, fmt.Errorf("packet length %d is less than header length %d", packLen, headerLen)
	}
	return Packet{
		ProtocolVersion: binary.BigEndian.Uint16(data[6:8]),
		Operation:       binary.BigEndian.Uint32(data[8:12]),
		Body:            data[headerLen:packLen],
	}, nil
}

// ParsePackets 解析数据包，处理压缩和分包
// 返回的包列表可能包含多个子包（当协议版本为 zlib/brotli 时）
func ParsePackets(data []byte) ([]Packet, error) {
	pkt, err := ParsePacket(data)
	if err != nil {
		return nil, err
	}

	switch pkt.ProtocolVersion {
	case ProtoPlain, ProtoPopularity:
		return []Packet{pkt}, nil
	case ProtoZlib:
		decompressed, err := zlibDecompress(pkt.Body)
		if err != nil {
			return nil, fmt.Errorf("zlib decompress: %w", err)
		}
		return slicePackets(decompressed), nil
	case ProtoBrotli:
		decompressed, err := brotliDecompress(pkt.Body)
		if err != nil {
			return nil, fmt.Errorf("brotli decompress: %w", err)
		}
		return slicePackets(decompressed), nil
	default:
		return []Packet{pkt}, nil
	}
}

// slicePackets 将解压后的数据切分为多个子包
func slicePackets(data []byte) []Packet {
	var packets []Packet
	cursor := 0
	for cursor+headerLen <= len(data) {
		packLen := binary.BigEndian.Uint32(data[cursor : cursor+4])
		if packLen < headerLen || uint64(packLen) > uint64(len(data)-cursor) {
			break
		}
		pkt, err := ParsePacket(data[cursor : cursor+int(packLen)])
		if err != nil {
			break
		}
		packets = append(packets, pkt)
		cursor += int(packLen)
	}
	return packets
}

func zlibDecompress(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func brotliDecompress(data []byte) ([]byte, error) {
	reader := brotli.NewReader(bytes.NewReader(data))
	return io.ReadAll(reader)
}
