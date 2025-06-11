//go:generate mockgen -package mock -destination mock/mock.go github.com/bililive-go/bililive-go/src/pkg/parser Parser
package parser

import (
	"context"
	"errors"

	"github.com/bililive-go/bililive-go/src/live"
)

type Builder interface {
	Build(cfg map[string]string) (Parser, error)
}

type Parser interface {
	ParseLiveStream(ctx context.Context, streamUrlInfo *live.StreamUrlInfo, live live.Live, file string) error
	Stop() error
}

type StatusParser interface {
	Parser
	Status() (map[string]string, error)
}

var m = make(map[string]Builder)

func Register(name string, b Builder) {
	m[name] = b
}

func New(name string, cfg map[string]string) (Parser, error) {
	builder, ok := m[name]
	if !ok {
		return nil, errors.New("unknown parser")
	}
	return builder.Build(cfg)
}
