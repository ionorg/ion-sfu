package sfu

import (
	"errors"

	"github.com/lucsky/cuid"
	sdptransform "github.com/notedit/sdp"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/rtc"
	transport "github.com/pion/ion-sfu/pkg/rtc/transport"
	"github.com/pion/webrtc/v2"

	pb "github.com/pion/ion-sfu/pkg/proto"
)

func (s *server) subscribe(mid string, payload *pb.SubscribeRequest_Connect) (*transport.WebRTCTransport, *pb.SubscribeReply_Connect, error) {
	log.Infof("subscribe->connect called: %v", payload.Connect)
	router := rtc.GetOrNewRouter(mid)

	if router == nil {
		return nil, nil, errors.New("subscribe->connect: router not found")
	}

	pub := router.GetPub().(*transport.WebRTCTransport)
	sdp := payload.Connect.Sdp

	rtcOptions := transport.RTCOptions{
		Subscribe: true,
	}

	if payload.Connect.Options != nil {
		if payload.Connect.Options.Bandwidth != 0 {
			rtcOptions.Bandwidth = payload.Connect.Options.Bandwidth
		}

		rtcOptions.TransportCC = payload.Connect.Options.Transportcc
	}

	tracks := pub.GetInTracks()
	rtcOptions.Ssrcpt = make(map[uint32]uint8)

	for ssrc, track := range tracks {
		rtcOptions.Ssrcpt[ssrc] = uint8(track.PayloadType())
	}

	sdpObj, err := sdptransform.Parse(string(sdp))
	if err != nil {
		log.Errorf("err=%v sdpObj=%v", err, sdpObj)
		return nil, nil, errors.New("subscribe: sdp parse failed")
	}

	ssrcPTMap := make(map[uint32]uint8)
	allowedCodecs := make([]uint8, 0, len(tracks))

	// for ssrc, track := range tracks {
	// 	// Find pt for track given track.Payload and sdp
	// 	ssrcPTMap[ssrc] = getSubPTForTrack(track, sdpObj)
	// 	allowedCodecs = append(allowedCodecs, ssrcPTMap[ssrc])
	// }

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

	// Build answer
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(sdp)}
	answer, err := sub.Answer(offer, rtcOptions)
	if err != nil {
		log.Errorf("err=%v answer=%v", err, answer)
		return nil, nil, errors.New("unsupported media type")
	}

	router.AddSub(mid, sub)

	log.Infof("subscribe->connect: mid %s, answer = %v", mid, answer)
	return sub, &pb.SubscribeReply_Connect{
		Connect: &pb.Connect{
			Sdp: []byte(answer.SDP),
		},
	}, nil
}
