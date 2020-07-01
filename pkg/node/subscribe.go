package sfu

import (
	"errors"
	"strconv"
	"strings"

	"github.com/lucsky/cuid"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/rtc"
	transport "github.com/pion/ion-sfu/pkg/rtc/transport"
	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"

	pb "github.com/pion/ion-sfu/pkg/proto"
)

func getSubCodec(track *webrtc.Track, sdp sdp.SessionDescription) uint8 {
	transform := transport.PayloadTransformMap()
	for _, md := range sdp.MediaDescriptions {
		if md.MediaName.Media != "audio" && md.MediaName.Media != "video" {
			continue
		}

		for _, format := range md.MediaName.Formats {
			pt, err := strconv.Atoi(format)

			if err != nil {
				return 0
			}

			if pt < 0 || pt > 255 {
				return 0
			}

			payloadType := uint8(pt)

			// 	If offer contains pub payload type, use that
			if track.PayloadType() == payloadType {
				return payloadType
			}

			payloadCodec, err := sdp.GetCodecForPayloadType(payloadType)
			if err != nil {
				return 0
			}

			// Otherwise look for first supported pt that can be transformed from pub
			log.Infof("%s %s", payloadCodec.Name, track.Codec().Name)
			if strings.EqualFold(payloadCodec.Name, track.Codec().Name) {
				for _, k := range transform[track.PayloadType()] {
					if payloadType == k {
						return k
					}
				}
			}
		}
	}

	return 0
}

func (s *server) subscribe(mid string, payload *pb.SubscribeRequest_Connect) (*transport.WebRTCTransport, *pb.SubscribeReply_Connect, error) {
	log.Infof("subscribe->connect called: %v", payload.Connect)
	router := rtc.GetOrNewRouter(mid)

	if router == nil {
		return nil, nil, errors.New("subscribe->connect: router not found")
	}

	pub := router.GetPub().(*transport.WebRTCTransport)
	offer := sdp.SessionDescription{}
	err := offer.Unmarshal(payload.Connect.Description.Sdp)

	if err != nil {
		log.Debugf("subscribe->connect: err=%v sdp=%v", err, offer)
		return nil, nil, errSdpParseFailed
	}

	rtcOptions := transport.RTCOptions{
		Subscribe:   true,
		Bandwidth:   payload.Connect.Options.Bandwidth,
		TransportCC: payload.Connect.Options.Transportcc,
		Ssrcpt:      make(map[uint32]uint8),
	}

	tracks := pub.GetInTracks()
	ssrcPTMap := make(map[uint32]uint8)
	allowedCodecs := make([]uint8, 0, len(tracks))

	for ssrc, track := range tracks {
		rtcOptions.Ssrcpt[ssrc] = uint8(track.PayloadType())

		// Find pt for track given track.Payload and sdp
		ssrcPTMap[ssrc] = getSubCodec(track, offer)
		allowedCodecs = append(allowedCodecs, ssrcPTMap[ssrc])
	}

	// Set media engine codecs based on found pts
	log.Infof("Allowed codecs %v", allowedCodecs)
	rtcOptions.Codecs = allowedCodecs

	sub := transport.NewWebRTCTransport(cuid.New(), rtcOptions)

	if sub == nil {
		return nil, nil, errors.New("subscribe->connect: transport.NewWebRTCTransport failed")
	}

	for ssrc, track := range tracks {
		// Get payload type from request track
		pt := track.PayloadType()
		if newPt, ok := ssrcPTMap[ssrc]; ok {
			// Override with "negotiated" PT
			pt = newPt
		}

		// I2AacsRLsZZriGapnvPKiKBcLi8rTrO1jOpq c84ded42-d2b0-4351-88d2-b7d240c33435
		//                streamID                        trackID
		log.Infof("AddTrack: codec:%s, ssrc:%d, pt:%d, streamID %s, trackID %s", track.Codec().MimeType, ssrc, pt, pub.ID(), track.ID())
		_, err := sub.AddSendTrack(ssrc, pt, pub.ID(), track.ID())
		if err != nil {
			log.Errorf("err=%v", err)
		}
	}

	answer, err := sub.Answer(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer, SDP: string(payload.Connect.Description.Sdp),
	}, rtcOptions)

	if err != nil {
		log.Debugf("subscribe->connect: error creating answer %v", err)
		return nil, nil, errWebRTCTransportAnswerFailed
	}

	router.AddSub(mid, sub)

	log.Infof("subscribe->connect: mid %s, answer = %v", mid, answer)
	return sub, &pb.SubscribeReply_Connect{
		Connect: &pb.Connect{
			Description: &pb.SessionDescription{
				Type: answer.Type.String(),
				Sdp:  []byte(answer.SDP),
			},
		},
	}, nil
}
