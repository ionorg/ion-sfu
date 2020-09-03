package sfu

import "errors"

var (
	errPeerConnectionInitFailed = errors.New("pc init failed")
	errPtNotSupported           = errors.New("payload type not supported")
	errMethodNotSupported       = errors.New("method not supported")
)
