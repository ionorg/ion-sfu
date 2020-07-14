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

const offer = `v=0
o=- 34578517 1594641025 IN IP4 0.0.0.0
s=-
t=0 0
a=fingerprint:sha-256 75:74:5A:A6:A4:E5:52:F4:A7:67:4C:01:C7:EE:91:3F:21:3D:A2:E3:53:7B:6F:30:86:F2:30:AA:65:FB:04:24
a=group:BUNDLE 0 1
m=video 9 UDP/TLS/RTP/SAVPF 96 98 102
c=IN IP4 0.0.0.0
a=setup:actpass
a=mid:0
a=ice-ufrag:hIbWqDfJNbsPgwLr
a=ice-pwd:LAZbSQFqoWOfcbsvzEZDEcgsufczOhtw
a=rtcp-mux
a=rtcp-rsize
a=rtpmap:96 VP8/90000
a=rtpmap:98 VP9/90000
a=rtpmap:102 H264/90000
a=fmtp:102 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f
a=ssrc:2596996162 cname:video
a=ssrc:2596996162 msid:video video
a=ssrc:2596996162 mslabel:video
a=ssrc:2596996162 label:video
a=msid:video video
a=sendrecv
a=candidate:foundation 1 udp 2130706431 10.205.126.148 52746 typ host generation 0
a=candidate:foundation 2 udp 2130706431 10.205.126.148 52746 typ host generation 0
a=candidate:foundation 1 udp 1694498815 108.29.157.141 59698 typ srflx raddr 0.0.0.0 rport 59698 generation 0
a=candidate:foundation 2 udp 1694498815 108.29.157.141 59698 typ srflx raddr 0.0.0.0 rport 59698 generation 0
a=end-of-candidates
m=application 9 DTLS/SCTP 5000
c=IN IP4 0.0.0.0
a=setup:actpass
a=mid:1
a=sendrecv
a=sctpmap:5000 webrtc-datachannel 1024
a=ice-ufrag:hIbWqDfJNbsPgwLr
a=ice-pwd:LAZbSQFqoWOfcbsvzEZDEcgsufczOhtw
a=candidate:foundation 1 udp 2130706431 10.205.126.148 52746 typ host generation 0
a=candidate:foundation 2 udp 2130706431 10.205.126.148 52746 typ host generation 0
a=candidate:foundation 1 udp 1694498815 108.29.157.141 59698 typ srflx raddr 0.0.0.0 rport 59698 generation 0
a=candidate:foundation 2 udp 1694498815 108.29.157.141 59698 typ srflx raddr 0.0.0.0 rport 59698 generation 0
a=end-of-candidates
`

func TestPublishReturnsSuccessfullyWithValidOffer(t *testing.T) {
	_, _, _, err := Publish(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offer,
	})

	if err != nil {
		t.Fatalf("Should not return error: %v", err)
	}
}
