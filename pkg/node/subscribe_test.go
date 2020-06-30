package sfu

import (
	"testing"

	"github.com/pion/rtp/codecs"
	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"
)

func TestGetSubCodecsReturnsCorrectAudioCodec(t *testing.T) {
	offer := sdp.SessionDescription{
		MediaDescriptions: []*sdp.MediaDescription{
			{
				MediaName: sdp.MediaName{
					Media:   "audio",
					Formats: []string{"0", "111"},
				},
				Attributes: []sdp.Attribute{
					sdp.NewAttribute("rtpmap:0 PCMU/8000", ""),
					sdp.NewAttribute("rtpmap:111 opus/48000", ""),
				},
			},
		},
	}

	c := webrtc.NewRTPCodec(webrtc.RTPCodecTypeAudio,
		"opus",
		48000,
		2, //According to RFC7587, Opus RTP streams must have exactly 2 channels.
		"minptime=10;useinbandfec=1",
		webrtc.DefaultPayloadTypeOpus,
		&codecs.OpusPayloader{})
	track, _ := webrtc.NewTrack(webrtc.DefaultPayloadTypeOpus, 1111, "msid", "label", c)
	codec := getSubCodec(track, offer)

	if codec != 111 {
		t.Fatal("Should return opus codec type")
	}
}

func TestGetSubCodecsReturnsCorrectVideoCodec(t *testing.T) {
	offer := sdp.SessionDescription{
		MediaDescriptions: []*sdp.MediaDescription{
			{
				MediaName: sdp.MediaName{
					Media:   "video",
					Formats: []string{"96", "121", "126", "97"},
				},
				Attributes: []sdp.Attribute{
					sdp.NewAttribute("rtpmap:121 VP9/90000", ""),
					sdp.NewAttribute("rtpmap:96 VP8/90000", ""),
					sdp.NewAttribute("rtpmap:126 H264/90000", ""),
					sdp.NewAttribute("rtpmap:97 H264/90000", ""),
				},
			},
		},
	}

	c := webrtc.NewRTPCodec(webrtc.RTPCodecTypeVideo,
		"VP8",
		90000,
		0,
		"",
		webrtc.DefaultPayloadTypeVP8,
		&codecs.VP8Payloader{})
	track, _ := webrtc.NewTrack(webrtc.DefaultPayloadTypeVP8, 1111, "msid", "label", c)

	codec := getSubCodec(track, offer)

	if codec != webrtc.DefaultPayloadTypeVP8 {
		t.Fatal("Should return VP8 codec type")
	}
}

func TestGetSubCodecsIgnoresH264PT126Codec(t *testing.T) {
	offer := sdp.SessionDescription{
		MediaDescriptions: []*sdp.MediaDescription{
			{
				MediaName: sdp.MediaName{
					Media:   "video",
					Formats: []string{"126", "97"},
				},
				Attributes: []sdp.Attribute{
					sdp.NewAttribute("rtpmap:126 H264/90000", ""),
					sdp.NewAttribute("rtpmap:97 H264/90000", ""),
				},
			},
		},
	}

	c := webrtc.NewRTPCodec(webrtc.RTPCodecTypeAudio,
		"H264",
		90000,
		0,
		"",
		webrtc.DefaultPayloadTypeH264,
		&codecs.H264Payloader{})
	track, _ := webrtc.NewTrack(webrtc.DefaultPayloadTypeH264, 1111, "msid", "label", c)

	codec := getSubCodec(track, offer)

	if codec != 97 {
		t.Fatal("Should return VP8 codec type")
	}
}
