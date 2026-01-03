package interfaces

import (
	"context"

	"github.com/sirupsen/logrus"
)

type Module interface {
	Start(ctx context.Context) error
	Close(ctx context.Context)
}

// TaskEnqueuer 任务入队接口
type TaskEnqueuer interface {
	EnqueueFixFlvTask(inputFile string) error
	EnqueueConvertMp4Task(inputFile string, deleteOriginal bool) error
}

type Logger struct {
	*logrus.Logger
}
