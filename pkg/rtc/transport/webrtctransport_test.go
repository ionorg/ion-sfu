package transport

import (
	"testing"

	"github.com/pion/ion-sfu/pkg/media"
	"github.com/pion/webrtc/v2"
	"github.com/stretchr/testify/assert"
)

func TestTypeReturnsWebRTCTransportType(t *testing.T) {
	me := media.Engine{}
	me.MediaEngine.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))
	pc, _ := api.NewPeerConnection(webrtc.Configuration{})

	pub := NewWebRTCTransport("pub", pc, &me)

	assert.Equal(t, pub.Type(), TypeWebRTCTransport)
}

func TestMediaEngineReturnsMediaEngine(t *testing.T) {
	me := &media.Engine{}
	me.MediaEngine.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))
	pc, _ := api.NewPeerConnection(webrtc.Configuration{})

	pub := NewWebRTCTransport("pub", pc, me)

	assert.Equal(t, pub.MediaEngine(), me)
}

func TestWebRTCTransportCloseHandlerOnlyOnce(t *testing.T) {
	me := media.Engine{}
	me.MediaEngine.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))
	pc, _ := api.NewPeerConnection(webrtc.Configuration{})

	pub := NewWebRTCTransport("pub", pc, &me)

	count := 0
	pub.OnClose(func() {
		count++
	})

	pub.Close()
	pub.Close()

	if count != 1 {
		t.Fatal("OnClose called on already closed transport")
	}
}
