package sfu

import (
	"testing"

	"github.com/pion/webrtc/v2"
	"github.com/stretchr/testify/assert"
)

const sdpValue = `v=0
o=- 884433216 1576829404 IN IP4 0.0.0.0
s=-
t=0 0
a=fingerprint:sha-256 1D:6B:6D:18:95:41:F9:BC:E4:AC:25:6A:26:A3:C8:09:D2:8C:EE:1B:7D:54:53:33:F7:E3:2C:0D:FE:7A:9D:6B
a=group:BUNDLE 0 1 2
m=audio 9 UDP/TLS/RTP/SAVPF 0 8 111 9
c=IN IP4 0.0.0.0
a=mid:0
a=rtpmap:0 PCMU/8000
a=rtpmap:8 PCMA/8000
a=rtpmap:111 opus/48000/2
a=fmtp:111 minptime=10;useinbandfec=1
a=rtpmap:9 G722/8000
a=ssrc:1823804162 cname:pion1
a=ssrc:1823804162 msid:pion1 audio
a=ssrc:1823804162 mslabel:pion1
a=ssrc:1823804162 label:audio
a=msid:pion1 audio
m=video 9 UDP/TLS/RTP/SAVPF 105 115 135
c=IN IP4 0.0.0.0
a=mid:1
a=rtpmap:105 VP8/90000
a=rtpmap:115 H264/90000
a=fmtp:115 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f
a=rtpmap:135 VP9/90000
a=ssrc:2949882636 cname:pion2
a=ssrc:2949882636 msid:pion2 video
a=ssrc:2949882636 mslabel:pion2
a=ssrc:2949882636 label:video
a=msid:pion2 video
m=application 9 DTLS/SCTP 5000
c=IN IP4 0.0.0.0
a=mid:2
a=sctpmap:5000 webrtc-datachannel 1024
`

const sdpMapValue = `v=0
o=- 884433216 1576829404 IN IP4 0.0.0.0
s=-
t=0 0
a=fingerprint:sha-256 1D:6B:6D:18:95:41:F9:BC:E4:AC:25:6A:26:A3:C8:09:D2:8C:EE:1B:7D:54:53:33:F7:E3:2C:0D:FE:7A:9D:6B
a=group:BUNDLE 0 1 2
m=audio 9 UDP/TLS/RTP/SAVPF 0 8 112 9
c=IN IP4 0.0.0.0
a=mid:0
a=rtpmap:0 PCMU/8000
a=rtpmap:8 PCMA/8000
a=rtpmap:112 opus/48000/2
a=fmtp:112 minptime=10;useinbandfec=1
a=rtpmap:9 G722/8000
a=ssrc:1823804162 cname:pion1
a=ssrc:1823804162 msid:pion1 audio
a=ssrc:1823804162 mslabel:pion1
a=ssrc:1823804162 label:audio
a=msid:pion1 audio
m=video 9 UDP/TLS/RTP/SAVPF 115 96 155
c=IN IP4 0.0.0.0
a=mid:1
a=rtpmap:115 VP8/90000
a=rtpmap:96 H264/90000
a=fmtp:96 level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f
a=rtpmap:155 VP9/90000
a=ssrc:2949882636 cname:pion2
a=ssrc:2949882636 msid:pion2 video
a=ssrc:2949882636 mslabel:pion2
a=ssrc:2949882636 label:video
a=msid:pion2 video
m=application 9 DTLS/SCTP 5000
c=IN IP4 0.0.0.0
a=mid:2
a=sctpmap:5000 webrtc-datachannel 1024
`

func TestPopulateFromSDP(t *testing.T) {
	m := MediaEngine{}
	assertCodecWithPayloadType := func(name string, payloadType uint8) {
		for _, c := range m.GetCodecsByName(name) {
			if c.PayloadType == payloadType && c.Name == name {
				return
			}
		}
		t.Fatalf("Failed to find codec(%s) with PayloadType(%d)", name, payloadType)
	}

	m.RegisterDefaultCodecs()
	assert.NoError(t, m.PopulateFromSDP(webrtc.SessionDescription{SDP: sdpValue}))

	assertCodecWithPayloadType(webrtc.Opus, 111)
	assertCodecWithPayloadType(webrtc.VP8, 105)
	assertCodecWithPayloadType(webrtc.H264, 115)
	assertCodecWithPayloadType(webrtc.VP9, 135)
}

// func TestMatchFrom(t *testing.T) {
// 	fe := Engine{}
// 	te := Engine{}

// 	assert.NoError(t, fe.PopulateFromSDP(webrtc.SessionDescription{SDP: sdpValue}))
// 	assert.NoError(t, te.PopulateFromSDP(webrtc.SessionDescription{SDP: sdpMapValue}))

// 	te.MapFromEngine(&fe)

// 	mappings := [][]uint8{{105, 115}, {115, 96}, {135, 155}, {111, 112}}

// 	for _, mapping := range mappings {
// 		to, ok := te.MapTo(mapping[0])
// 		assert.True(t, ok)
// 		assert.Equal(t, to, mapping[1])
// 	}
// }

// func TestNoMatch(t *testing.T) {
// 	fe := &Engine{}
// 	fe.MediaEngine.RegisterCodec(webrtc.NewRTPVP8Codec(webrtc.DefaultPayloadTypeVP8, 90000))
// 	te := &Engine{}
// 	te.MediaEngine.RegisterCodec(webrtc.NewRTPVP9Codec(webrtc.DefaultPayloadTypeVP9, 90000))

// 	te.MapFromEngine(fe)

// 	_, ok := te.MapTo(webrtc.DefaultPayloadTypeVP8)

// 	assert.False(t, ok)
// }

// func TestNoMatchFromMapToWhenNotInitilaized(t *testing.T) {
// 	e := Engine{}
// 	_, ok := e.MapTo(97)
// 	assert.False(t, ok)
// }
