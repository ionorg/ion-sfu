package sfu

import (
	"errors"
	"strings"

	"github.com/lucsky/cuid"
	sdptransform "github.com/notedit/sdp"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/rtc"
	transport "github.com/pion/ion-sfu/pkg/rtc/transport"
	"github.com/pion/webrtc/v2"

	pb "github.com/pion/ion-sfu/pkg/proto"
)

func (s *server) publish(payload *pb.PublishRequest_Connect) (*transport.WebRTCTransport, *pb.PublishReply_Connect, error) {
	mid := cuid.New()
	options := payload.Connect.Options
	sdp := payload.Connect.Description.Sdp

	if sdp == "" {
		return nil, nil, errors.New("publish->connect: sdp invalid")
	}

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}

	rtcOptions := transport.RTCOptions{
		Publish:     true,
		Codec:       options.Codec,
		Bandwidth:   options.Bandwidth,
		TransportCC: options.Transportcc,
	}

	videoCodec := strings.ToUpper(rtcOptions.Codec)

	sdpObj, err := sdptransform.Parse(offer.SDP)
	if err != nil {
		log.Debugf("err=%v sdpObj=%v", err, sdpObj)
		return nil, nil, errors.New("publish->connect: sdp parse failed")
	}

	allowedCodecs := make([]uint8, 0)
	for _, s := range sdpObj.GetStreams() {
		for _, track := range s.GetTracks() {
			pt, _ := getPubPTForTrack(videoCodec, track, sdpObj)

			if len(track.GetSSRCS()) == 0 {
				return nil, nil, errors.New("publish->connect: ssrc not found")
			}
			allowedCodecs = append(allowedCodecs, pt)
		}
	}

	rtcOptions.Codecs = allowedCodecs
	pub := transport.NewWebRTCTransport(mid, rtcOptions)
	if pub == nil {
		return nil, nil, errors.New("publish->connect: transport.NewWebRTCTransport failed")
	}

	router := rtc.AddRouter(mid)
	answer, err := pub.Answer(offer, rtcOptions)
	if err != nil {
		log.Debugf("publish->connect: error creating answer. err=%v answer=%v", err, answer)
		return nil, nil, err
	}

	log.Debugf("publish->connect: answer => %v", answer)

	router.AddPub(pub)

	return pub, &pb.PublishReply_Connect{
		Connect: &pb.Connect{
			Description: &pb.SessionDescription{
				Type: answer.Type.String(),
				Sdp:  answer.SDP,
			},
		},
	}, nil
}
