package live

import (
	"errors"
)

var (
	ErrRoomNotExist     = errors.New("room not exists")
	ErrRoomUrlIncorrect = errors.New("room url incorrect")
	ErrInternalError    = errors.New("internal error")
	ErrNotImplemented   = errors.New("not implemented")
	ErrLiveOffline      = errors.New("live is offline")
)
