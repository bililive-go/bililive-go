package interfaces

import (
	"context"

	"github.com/sirupsen/logrus"
)

type Module interface {
	Start(ctx context.Context) error
	Close(ctx context.Context)
}

type Logger struct {
	*logrus.Logger
}
