package sfu

//go:generate go run github.com/matryer/moq -out sender_mock_test.generated.go . Sender

import (
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// SenderType determines the type of a sender
type SenderType int

const (
	SimpleSenderType SenderType = iota + 1
	SimulcastSenderType
	SVCSenderType
)

// Sender defines a interface for a track receivers
type Sender interface {
	ID() string
	Start()
	Close()
	Kind() webrtc.RTPCodecType
	Type() SenderType
	Track() *webrtc.Track
	Transceiver() *webrtc.RTPTransceiver
	Mute(val bool)
	WriteRTP(*rtp.Packet)
	CurrentSpatialLayer() uint8
	OnCloseHandler(fn func())
	// Simulcast/SVC events
	SwitchSpatialLayer(layer uint8)
	SwitchTemporalLayer(layer uint8)
}
