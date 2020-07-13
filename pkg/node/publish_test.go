package sfu

import (
	"testing"

	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"
)

func TestPublishReturnsErrorWithInvalidSDP(t *testing.T) {
	_, _, _, err := Publish(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  "invalid",
	})

	if err != errSdpParseFailed {
		t.Fatal("Should return error on invalid sdp")
	}
}

func TestPublishReturnsErrorWithInvalidCodecsInSDP(t *testing.T) {
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

	_, _, _, err := Publish(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(marshalled),
	})

	if err != errSdpParseFailed {
		t.Fatal("Should return error on invalid sdp")
	}
}
