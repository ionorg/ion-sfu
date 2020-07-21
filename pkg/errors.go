package sfu

import "errors"

var (
	errSdpParseFailed           = errors.New("sdp parse failed")
	errPeerConnectionInitFailed = errors.New("pc init failed")
	errChanClosed               = errors.New("channel closed")
	errPtNotSupported           = errors.New("payloadtype not supported")
)
