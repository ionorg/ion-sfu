package sfu

import "errors"

var (
	errSdpParseFailed            = errors.New("sdp parse failed")
	errPeerConnectionInitFailed  = errors.New("pc init failed")
	errWebRTCTransportInitFailed = errors.New("WebRTCTransport init failed")
	errRouterNotFound            = errors.New("router not found")
	errChanClosed                = errors.New("channel closed")
	errPtNotSupported            = errors.New("payloadtype not supported")
)
