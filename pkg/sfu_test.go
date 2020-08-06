package sfu

import (
	"testing"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

func TestSFU(t *testing.T) {
	s := NewSFU(Config{
		Log: log.Config{
			Level: "error",
		},
		WebRTC: WebRTCConfig{},
		Receiver: ReceiverConfig{
			Video: WebRTCVideoReceiverConfig{},
		},
	})

	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	remote, err := api.NewPeerConnection(cfg)
	assert.NoError(t, err)

	offer, err := remote.CreateOffer(nil)
	assert.NoError(t, err)
	err = remote.SetLocalDescription(offer)
	assert.NoError(t, err)

	transport, err := s.NewWebRTCTransport(1, offer)
	assert.NotNil(t, transport)
	assert.NoError(t, err)
}
