//go:generate go run go.uber.org/mock/mockgen -package mock -destination mock/mock.go github.com/bililive-go/bililive-go/src/pkg/parser Parser
package parser

import (
	"context"
	"errors"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
)

type Builder interface {
	Build(cfg map[string]string, logger *livelogger.LiveLogger) (Parser, error)
}

type Parser interface {
	ParseLiveStream(ctx context.Context, streamUrlInfo *live.StreamUrlInfo, live live.Live, file string) error
	Stop() error
}

type StatusParser interface {
	Parser
	Status() (map[string]interface{}, error)
}

// PIDProvider 提供进程 PID 的接口
// 用于获取 parser 启动的子进程 PID
type PIDProvider interface {
	GetPID() int
}

// SegmentRequester 提供分段请求的接口
// 用于请求在下一个关键帧处分段（仅在使用 FLV 代理时有效）
type SegmentRequester interface {
	// RequestSegment 请求在下一个关键帧处分段
	// 返回 true 表示请求已接受，false 表示不支持或请求被拒绝
	RequestSegment() bool
	// HasFlvProxy 检查当前是否使用 FLV 代理
	HasFlvProxy() bool
}

var m = make(map[string]Builder)

func Register(name string, b Builder) {
	m[name] = b
}

func New(name string, cfg map[string]string, logger *livelogger.LiveLogger) (Parser, error) {
	builder, ok := m[name]
	if !ok {
		return nil, errors.New("unknown parser")
	}
	return builder.Build(cfg, logger)
}
