package sfu

import (
	"math/rand"
	"testing"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/media"
	"github.com/pion/ion-sfu/pkg/rtc"
	"github.com/pion/ion-sfu/pkg/rtc/plugins"
	"github.com/pion/webrtc/v2"
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
	pub, _ := api.NewPeerConnection(webrtc.Configuration{})

	track, _ := pub.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	pub.AddTrack(track)

	offer, _ := pub.CreateOffer(nil)
	pub.SetLocalDescription(offer)

	mid, _, answer, _ := Publish(offer)

	pub.SetRemoteDescription(*answer)

	sub, _ := api.NewPeerConnection(webrtc.Configuration{})
	sub.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})

	// var subTrack *webrtc.Track
	sub.OnTrack(func(track *webrtc.Track, r *webrtc.RTPReceiver) {
		t.Logf("on track %v", track)
	})

	offer, _ = sub.CreateOffer(nil)
	sub.SetLocalDescription(offer)

	_, _, answer, _ = Subscribe(mid, offer)

	sub.SetRemoteDescription(*answer)

	rawPkt := []byte{
		0x90, 0xe0, 0x69, 0x8f, 0xd9, 0xc2, 0x93, 0xda, 0x1c, 0x64,
		0x27, 0x82, 0x00, 0x01, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x98, 0x36, 0xbe, 0x88, 0x9e,
	}

	_, _ = track.Write(rawPkt)

	time.Sleep(time.Second)
}
