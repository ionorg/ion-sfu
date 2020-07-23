package sfu

import "errors"

var (
	errSdpParseFailed           = errors.New("sdp parse failed")
	errPeerConnectionInitFailed = errors.New("pc init failed")
	errChanClosed               = errors.New("channel closed")
	errPtNotSupported           = errors.New("payload type not supported")
	errMethodNotSupported       = errors.New("method not supported")
	errReceiverClosed           = errors.New("receiver closed")
)
