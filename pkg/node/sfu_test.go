package sfu

import (
	"math/rand"
	"testing"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/media"
	"github.com/pion/ion-sfu/pkg/rtc"
	"github.com/pion/ion-sfu/pkg/rtc/plugins"
	"github.com/pion/webrtc/v2"
	"github.com/stretchr/testify/assert"
)

var conf = Config{
	Router: rtc.RouterConfig{
		MinBandwidth: 100000,
		MaxBandwidth: 5000000,
		REMBFeedback: false,
	},
	Plugins: plugins.Config{
		On: true,
		JitterBuffer: plugins.JitterBufferConfig{
			On:            true,
			TCCOn:         false,
			REMBCycle:     2,
			PLICycle:      1,
			MaxBandwidth:  1000,
			MaxBufferTime: 1000,
		},
		RTPForwarder: plugins.RTPForwarderConfig{
			On: false,
		},
	},
	WebRTC: WebRTCConfig{},
	Log: log.Config{
		Level: "error",
	},
}

func init() {
	Init(conf)
}

func TestPubSub(t *testing.T) {
	me := &media.Engine{}
	me.MediaEngine.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))
	pub, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)
	sub, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)
	_, err = sub.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendrecv,
	})
	assert.NoError(t, err)

	track, err := sub.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = pub.AddTrack(track)
	assert.NoError(t, err)

	offer, err := pub.CreateOffer(nil)
	assert.NoError(t, err)
	err = pub.SetLocalDescription(offer)
	assert.NoError(t, err)

	mid, _, answer, err := Publish(offer)
	assert.NoError(t, err)
	err = pub.SetRemoteDescription(*answer)
	assert.NoError(t, err)

	sub.OnTrack(func(track *webrtc.Track, r *webrtc.RTPReceiver) {
		t.Logf("on track %v", track)
	})

	offer, err = sub.CreateOffer(nil)
	assert.NoError(t, err)
	err = sub.SetLocalDescription(offer)
	assert.NoError(t, err)

	_, _, answer, err = Subscribe(mid, offer)
	assert.NoError(t, err)

	err = sub.SetRemoteDescription(*answer)
	assert.NoError(t, err)
}
