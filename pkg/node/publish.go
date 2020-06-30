package sfu

import (
	"fmt"
	"strconv"

	"github.com/lucsky/cuid"
	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/rtc"
	transport "github.com/pion/ion-sfu/pkg/rtc/transport"

	pb "github.com/pion/ion-sfu/pkg/proto"
)

func getPubCodecs(sdp sdp.SessionDescription) ([]uint8, error) {
	allowedCodecs := make([]uint8, 0)
	for _, md := range sdp.MediaDescriptions {
		if md.MediaName.Media != "audio" && md.MediaName.Media != "video" {
			continue
		}

		for _, format := range md.MediaName.Formats {
			pt, err := strconv.Atoi(format)
			if err != nil {
				return nil, fmt.Errorf("format parse error")
			}

			if pt < 0 || pt > 255 {
				return nil, fmt.Errorf("payload type out of range: %d", pt)
			}

			payloadType := uint8(pt)
			payloadCodec, err := sdp.GetCodecForPayloadType(payloadType)
			if err != nil {
				return nil, fmt.Errorf("could not find codec for payload type %d", payloadType)
			}

			if md.MediaName.Media == "audio" {
				if payloadCodec.Name == webrtc.Opus {
					allowedCodecs = append(allowedCodecs, payloadType)
					break
				}
			} else {
				// skip 126 for pub, chrome sub decode will fail when H264 playload type is 126
				if payloadCodec.Name == webrtc.H264 && payloadType == 126 {
					continue
				}
				allowedCodecs = append(allowedCodecs, payloadType)
				break
			}
		}
	}

	return allowedCodecs, nil
}

func (s *server) publish(payload *pb.PublishRequest_Connect) (*transport.WebRTCTransport, *pb.PublishReply_Connect, error) {
	mid := cuid.New()
	options := payload.Connect.Options
	offer := sdp.SessionDescription{}
	err := offer.Unmarshal(payload.Connect.Sdp)

	if err != nil {
		log.Debugf("publish->connect: err=%v sdp=%v", err, offer)
		return nil, nil, errSdpParseFailed
	}

	rtcOptions := transport.RTCOptions{
		Publish:     true,
		Bandwidth:   options.Bandwidth,
		TransportCC: options.Transportcc,
	}

	codecs, err := getPubCodecs(offer)

	if err != nil {
		log.Debugf("publish->connect: err=%v", err)
		return nil, nil, errSdpParseFailed
	}

	rtcOptions.Codecs = codecs
	pub := transport.NewWebRTCTransport(mid, rtcOptions)
	if pub == nil {
		return nil, nil, errWebRTCTransportInitFailed
	}

	router := rtc.AddRouter(mid)

	answer, err := pub.Answer(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer, SDP: string(payload.Connect.Sdp),
	}, rtcOptions)

	if err != nil {
		log.Debugf("publish->connect: error creating answer %v", err)
		return nil, nil, errWebRTCTransportAnswerFailed
	}

	log.Debugf("publish->connect: answer => %v", answer)

	router.AddPub(pub)

	return pub, &pb.PublishReply_Connect{
		Connect: &pb.Connect{
			Sdp: []byte(answer.SDP),
		},
	}, nil
}
