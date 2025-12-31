package instance

import (
	"sync"

	"github.com/bililive-go/bililive-go/src/interfaces"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/logstore"
	"github.com/bililive-go/bililive-go/src/types"
	"github.com/bluele/gcache"
)

type Instance struct {
	WaitGroup       sync.WaitGroup
	Lives           map[types.LiveID]live.Live
	Cache           gcache.Cache
	Server          interfaces.Module
	EventDispatcher interfaces.Module
	ListenerManager interfaces.Module
	RecorderManager interfaces.Module
	LogStore        *logstore.LogStore // 新增日志存储
}
