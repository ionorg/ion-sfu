package sfu

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v3"
)

const (
	mediaNameAudio = "audio"
	mediaNameVideo = "video"
	IOSH264Fmtp    = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"
)

var (
	rtcpfb = []webrtc.RTCPFeedback{
		{Type: webrtc.TypeRTCPFBCCM},
		{Type: webrtc.TypeRTCPFBNACK},
		{Type: "nack pli"},
	}
)

// MediaEngine handles stream codecs
type MediaEngine struct {
	webrtc.MediaEngine
}

// PopulateFromSDP finds all codecs in sd and adds them to m, using the dynamic
// payload types and parameters from sd.
// PopulateFromSDP is intended for use when answering a request.
// The offerer sets the PayloadTypes for the connection.
// PopulateFromSDP allows an answerer to properly match the PayloadTypes from the offerer.
// A MediaEngine populated by PopulateFromSDP should be used only for a single session.
func (e *MediaEngine) PopulateFromSDP(sd webrtc.SessionDescription, codecs []string) error {
	sdp := sdp.SessionDescription{}
	if err := sdp.Unmarshal([]byte(sd.SDP)); err != nil {
		return err
	}

	for _, md := range sdp.MediaDescriptions {
		if md.MediaName.Media != mediaNameAudio && md.MediaName.Media != mediaNameVideo {
			continue
		}

		for _, format := range md.MediaName.Formats {
			pt, err := strconv.Atoi(format)
			if err != nil {
				return fmt.Errorf("format parse error")
			}

			payloadType := uint8(pt)
			payloadCodec, err := sdp.GetCodecForPayloadType(payloadType)
			if err != nil {
				return fmt.Errorf("could not find codec for payload type %d", payloadType)
			}

			fmt.Printf("codecs=%+v\n", codecs)
			for _, c := range codecs {
				var codec *webrtc.RTPCodec
				switch {
				case strings.EqualFold(payloadCodec.Name, webrtc.Opus) && strings.EqualFold(payloadCodec.Name, c):
					codec = webrtc.NewRTPOpusCodec(payloadType, payloadCodec.ClockRate)
				case strings.EqualFold(payloadCodec.Name, webrtc.VP8) && strings.EqualFold(payloadCodec.Name, c):
					codec = webrtc.NewRTPVP8CodecExt(payloadType, payloadCodec.ClockRate, rtcpfb, payloadCodec.Fmtp)
				case strings.EqualFold(payloadCodec.Name, webrtc.VP9) && strings.EqualFold(payloadCodec.Name, c):
					codec = webrtc.NewRTPVP9CodecExt(payloadType, payloadCodec.ClockRate, rtcpfb, payloadCodec.Fmtp)
				case strings.EqualFold(payloadCodec.Name, webrtc.H264) && strings.EqualFold(payloadCodec.Name, c) && strings.EqualFold(payloadCodec.Fmtp, IOSH264Fmtp):
					codec = webrtc.NewRTPH264CodecExt(payloadType, payloadCodec.ClockRate, rtcpfb, payloadCodec.Fmtp)
				default:
					// ignoring other codecs
					continue
				}
				fmt.Printf("e.RegisterCodec  %+v\n", codec)
				e.RegisterCodec(codec)
			}

		}
	}

	return nil
}
