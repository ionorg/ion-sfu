package media

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"
)

const (
	mediaNameAudio = "audio"
	mediaNameVideo = "video"
)

var (
	rtcpfb = []webrtc.RTCPFeedback{
		{Type: webrtc.TypeRTCPFBGoogREMB},
		{Type: webrtc.TypeRTCPFBCCM},
		{Type: webrtc.TypeRTCPFBNACK},
		{Type: "nack pli"},
		{Type: webrtc.TypeRTCPFBTransportCC},
	}
)

// Engine handles stream codecs
type Engine struct {
	webrtc.MediaEngine
	mapping map[uint8]uint8
}

// PopulateFromSDP finds all codecs in sd and adds them to m, using the dynamic
// payload types and parameters from sd.
// PopulateFromSDP is intended for use when answering a request.
// The offerer sets the PayloadTypes for the connection.
// PopulateFromSDP allows an answerer to properly match the PayloadTypes from the offerer.
// A MediaEngine populated by PopulateFromSDP should be used only for a single session.
func (e *Engine) PopulateFromSDP(sd webrtc.SessionDescription) error {
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

			var codec *webrtc.RTPCodec
			switch {
			case strings.EqualFold(payloadCodec.Name, webrtc.Opus):
				codec = webrtc.NewRTPOpusCodec(payloadType, payloadCodec.ClockRate)
			case strings.EqualFold(payloadCodec.Name, webrtc.VP8):
				codec = webrtc.NewRTPVP8CodecExt(payloadType, payloadCodec.ClockRate, rtcpfb, payloadCodec.Fmtp)
			case strings.EqualFold(payloadCodec.Name, webrtc.VP9):
				codec = webrtc.NewRTPVP9CodecExt(payloadType, payloadCodec.ClockRate, rtcpfb, payloadCodec.Fmtp)
			case strings.EqualFold(payloadCodec.Name, webrtc.H264):
				codec = webrtc.NewRTPH264CodecExt(payloadType, payloadCodec.ClockRate, rtcpfb, payloadCodec.Fmtp)
			default:
				// ignoring other codecs
				continue
			}

			e.RegisterCodec(codec)
		}
	}
	return nil
}

// MapFromEngine finds codec payload type mappings between two MediaEngines.
// If a codec is supported by both media engines, a mapping is established.
// Payload type mappings are necessary when a pub and subs payload type differ for the same codec.
func (e *Engine) MapFromEngine(from *Engine) {
	if e.mapping == nil {
		e.mapping = make(map[uint8]uint8)
	}

	for _, codec := range from.MediaEngine.GetCodecsByKind(webrtc.RTPCodecTypeVideo) {
		to := e.GetCodecsByName(codec.Name)

		if len(to) > 0 {
			// Just take first?
			e.mapping[codec.PayloadType] = to[0].PayloadType
		}
	}
}

// MapTo maps an incoming payload type to one supported by
// this media engine.
func (e *Engine) MapTo(pt uint8) (uint8, bool) {
	if e.mapping == nil {
		return 0, false
	}

	destType, ok := e.mapping[pt]
	return destType, ok
}
