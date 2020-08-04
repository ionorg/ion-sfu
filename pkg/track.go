package sfu

import (
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// Track represents a audio/video track
type Track interface {
	Kind() webrtc.RTPCodecType
	SSRC() uint32
	ID() string
	Label() string
	Codec() *webrtc.RTPCodec
	PayloadType() uint8
	WriteRTP(*rtp.Packet) error
}
