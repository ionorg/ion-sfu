package sfu

//go:generate go run github.com/matryer/moq -out sender_mock_test.generated.go . Sender

import (
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// Sender defines a interface for a track receivers
type Sender interface {
	ID() string
	Close()
	Kind() webrtc.RTPCodecType
	Mute(val bool)
	WriteRTP(*rtp.Packet)
	CurrentSpatialLayer() uint8
	OnCloseHandler(fn func())
	// Simulcast/SVC events
	SwitchSpatialLayer(layer uint8)
	SwitchTemporalLayer(layer uint8)
}
