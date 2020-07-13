package sfu

import (
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
}

var pcconf = webrtc.Configuration{
	ICEServers: []webrtc.ICEServer{
		{
			URLs: []string{"stun:stun.l.google.com:19302"},
		},
	},
}

// func TestPublishThenSubscribeCreatedEmptyPeerConnection(t *testing.T) {
// 	Init(conf)

// 	mediaEngine := webrtc.MediaEngine{}
// 	mediaEngine.RegisterDefaultCodecs()
// 	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine))

// 	// Create pub
// 	pubPC, _ := api.NewPeerConnection(pcconf)
// 	videoTrack, _ := pubPC.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
// 	pubPC.AddTrack(videoTrack)
// 	pubOffer, _ := pubPC.CreateOffer(nil)
// 	pub, pubAnswer, _ := Publish(pubOffer)
// 	pubPC.SetRemoteDescription(*pubAnswer)

// 	mid := pub.ID()

// 	// Create sub
// 	subPC, _ := api.NewPeerConnection(pcconf)
// 	subPC.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RtpTransceiverInit{
// 		Direction: webrtc.RTPTransceiverDirectionRecvonly,
// 	})
// 	subOffer, _ := subPC.CreateOffer(nil)
// 	_, subAnswer, _ := Subscribe(mid, subOffer)
// 	subPC.SetRemoteDescription(*subAnswer)

// 	// if err != errSdpParseFailed {
// 	// 	t.Fatal("Should return error on invalid sdp")
// 	// }
// }
