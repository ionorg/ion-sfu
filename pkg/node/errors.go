package sfu

import "errors"

var (
	errSdpParseFailed              = errors.New("sdp parse failed")
	errPeerConnectionInitFailed    = errors.New("pc init failed")
	errWebRTCTransportInitFailed   = errors.New("WebRTCTransport init failed")
	errWebRTCTransportAnswerFailed = errors.New("creating answer failed")
)
