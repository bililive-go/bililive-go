package instance

import (
	"sync"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/interfaces"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/simplecache"
	"github.com/bililive-go/bililive-go/src/types"
)

type Instance struct {
	WaitGroup       sync.WaitGroup
	Config          *configs.Config
	Logger          *interfaces.Logger
	Lives           map[types.LiveID]live.Live
	Cache           simplecache.Cache
	Server          interfaces.Module
	EventDispatcher interfaces.Module
	ListenerManager interfaces.Module
	RecorderManager interfaces.Module
}
