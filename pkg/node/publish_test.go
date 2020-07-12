package sfu

import (
	"testing"

	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"
)

func TestPublishReturnsErrorWithInvalidSDP(t *testing.T) {
	_, _, err := Publish(webrtc.SessionDescription{
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

	_, _, err := Publish(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(marshalled),
	})

	if err != errSdpParseFailed {
		t.Fatal("Should return error on invalid sdp")
	}
}

func TestGetPubCodecsReturnsCorrectAudioCodec(t *testing.T) {
	offer := sdp.SessionDescription{
		MediaDescriptions: []*sdp.MediaDescription{
			{
				MediaName: sdp.MediaName{
					Media:   "audio",
					Formats: []string{"0", "96"},
				},
				Attributes: []sdp.Attribute{
					sdp.NewAttribute("rtpmap:0 PCMU/8000", ""),
					sdp.NewAttribute("rtpmap:96 opus/48000", ""),
				},
			},
		},
	}

	allowedCodecs, _ := getPubCodecs(offer)

	if allowedCodecs[0] != 96 {
		t.Fatal("Should return opus codec type")
	}
}

func TestGetPubCodecsReturnsCorrectVideoCodec(t *testing.T) {
	offer := sdp.SessionDescription{
		MediaDescriptions: []*sdp.MediaDescription{
			{
				MediaName: sdp.MediaName{
					Media:   "video",
					Formats: []string{"120", "121", "126", "97"},
				},
				Attributes: []sdp.Attribute{
					sdp.NewAttribute("rtpmap:120 VP8/90000", ""),
					sdp.NewAttribute("rtpmap:121 VP9/90000", ""),
					sdp.NewAttribute("rtpmap:126 H264/90000", ""),
					sdp.NewAttribute("rtpmap:97 H264/90000", ""),
				},
			},
		},
	}

	allowedCodecs, _ := getPubCodecs(offer)

	if allowedCodecs[0] != 120 {
		t.Fatal("Should return VP8 codec type")
	}
}

func TestGetPubCodecsIgnoresH264PT126Codec(t *testing.T) {
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

	allowedCodecs, _ := getPubCodecs(offer)

	if allowedCodecs[0] != 97 {
		t.Fatal("Should return VP8 codec type")
	}
}

func TestGetPubCodecsReturnsVideoAndAudio(t *testing.T) {
	offer := sdp.SessionDescription{
		MediaDescriptions: []*sdp.MediaDescription{
			{
				MediaName: sdp.MediaName{
					Media:   "audio",
					Formats: []string{"96"},
				},
				Attributes: []sdp.Attribute{
					sdp.NewAttribute("rtpmap:96 opus/48000", ""),
				},
			},
			{
				MediaName: sdp.MediaName{
					Media:   "video",
					Formats: []string{"97"},
				},
				Attributes: []sdp.Attribute{
					sdp.NewAttribute("rtpmap:97 H264/90000", ""),
				},
			},
		},
	}

	allowedCodecs, _ := getPubCodecs(offer)

	if allowedCodecs[0] != 96 || allowedCodecs[1] != 97 {
		t.Fatal("Should return VP8 codec type")
	}
}
