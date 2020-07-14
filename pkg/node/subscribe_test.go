package sfu

import (
	"testing"

	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"
)

func TestSubscribeReturnsErrorWithInvalidSDP(t *testing.T) {
	_, _, _, err := Subscribe("mid", webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  "invalid",
	})

	if err != errSdpParseFailed {
		t.Fatal("Should return error on invalid sdp")
	}
}

func TestSubscribeReturnsErrorWithInvalidCodecsInSDP(t *testing.T) {
	offer := sdp.SessionDescription{
		MediaDescriptions: []*sdp.MediaDescription{
			{
				MediaName: sdp.MediaName{
					Media:   "audio",
					Formats: []string{"96"},
				},
				Attributes: []sdp.Attribute{},
			},
		},
	}

	marshalled, _ := offer.Marshal()

	_, _, _, err := Subscribe("mid", webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(marshalled),
	})

	if err != errSdpParseFailed {
		t.Fatal("Should return error on invalid sdp")
	}
}

func TestSubscribeReturnsErrorWithInvalidMid(t *testing.T) {
	_, _, _, err := Subscribe("mid", webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer,
	})

	if err != errRouterNotFound {
		t.Fatal("Should return error on invalid mid")
	}
}

func TestSubscribeReturnsSuccessfully(t *testing.T) {
	mid, _, _, err := Publish(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer,
	})

	if err != nil {
		t.Fatal("Publish should return successfully")
	}

	_, _, _, err = Subscribe(mid, webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer,
	})

	if err != nil {
		t.Fatal("Subscribe should return successfully")
	}
}
