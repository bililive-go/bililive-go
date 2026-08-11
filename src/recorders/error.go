package recorders

import "errors"

var (
	ErrRecorderExist          = errors.New("recorder is exist")
	ErrRecorderNotExist       = errors.New("recorder is not exist")
	ErrParserNotSupportStatus = errors.New("parser not support get status")
	ErrManagerClosed          = errors.New("recorder manager is closed")
)
