package instance

import (
	"sync"

	"github.com/bililive-go/bililive-go/src/interfaces"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/types"
	"github.com/bluele/gcache"
)

type Instance struct {
	WaitGroup        sync.WaitGroup
	Lives            map[types.LiveID]live.Live
	Cache            gcache.Cache
	Server           interfaces.Module
	EventDispatcher  interfaces.Module
	ListenerManager  interfaces.Module
	RecorderManager  interfaces.Module
	PipelineManager  interfaces.Module // 后处理管道管理器
	LiveStateManager interface{}       // 直播间状态持久化管理器 (*livestate.Manager)
	LiveStateStore   interface{}       // 直播间状态存储 (livestate.Store)
	IOStatsModule    interfaces.Module // IO 统计模块 (*iostats.Module)
}
