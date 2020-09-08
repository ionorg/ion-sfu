package sfu

import (
	"testing"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/transport/test"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

func TestSFU(t *testing.T) {
	lim := test.TimeOut(time.Second * 20)
	defer lim.Stop()

	// report := test.CheckRoutines(t)
	// defer report()

	s := NewSFU(Config{
		Log: log.Config{
			Level: "error",
			Stats: true,
		},
		WebRTC: WebRTCConfig{},
		Receiver: ReceiverConfig{
			Video: WebRTCVideoReceiverConfig{},
		},
	})

	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	remote, err := api.NewPeerConnection(conf.configuration)
	assert.NoError(t, err)

	offer, err := remote.CreateOffer(nil)
	assert.NoError(t, err)
	err = remote.SetLocalDescription(offer)
	assert.NoError(t, err)

	engine := MediaEngine{}
	err = engine.PopulateFromSDP(offer)
	assert.NoError(t, err)

	transport, err := s.NewWebRTCTransport("test session", engine)
	assert.NotNil(t, transport)
	assert.NoError(t, err)

	remote.Close()
	transport.Close()

	s.Stop()
}
