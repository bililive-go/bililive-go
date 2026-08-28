package listeners

import "errors"

var (
	ErrListenerExist             = errors.New("this live has a listener")
	ErrListenerNotExist          = errors.New("this live has not a listener")
	ErrInitializingResultExpired = errors.New("the initializing result is no longer current")
)
