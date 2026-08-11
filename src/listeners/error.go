package listeners

import "errors"

var (
	ErrListenerExist    = errors.New("this live has a listener")
	ErrListenerNotExist = errors.New("this live has not a listener")
	ErrManagerClosed    = errors.New("listener manager is closed")
)
