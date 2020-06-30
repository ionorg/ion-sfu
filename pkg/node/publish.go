package sfu

import (
	"errors"

	"github.com/lucsky/cuid"
	sdp "github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"

	// sdptransform "github.com/notedit/sdp"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/rtc"
	transport "github.com/pion/ion-sfu/pkg/rtc/transport"

	pb "github.com/pion/ion-sfu/pkg/proto"
)

var (
	errSdpParseFailed = errors.New("sdp parse failed")
)

func (s *server) publish(payload *pb.PublishRequest_Connect) (*transport.WebRTCTransport, *pb.PublishReply_Connect, error) {
	mid := cuid.New()
	options := payload.Connect.Options
	parsed := sdp.SessionDescription{}
	err := parsed.Unmarshal(payload.Connect.Sdp)

	if err != nil {
		log.Debugf("err=%v sdp=%v", err, parsed)
		return nil, nil, errSdpParseFailed
	}

	rtcOptions := transport.RTCOptions{
		Publish:     true,
		Codec:       options.Codec,
		Bandwidth:   options.Bandwidth,
		TransportCC: options.Transportcc,
	}

	// videoCodec := strings.ToUpper(rtcOptions.Codec)

	allowedCodecs, err := getCodecs(parsed)
	log.Infof("%v", allowedCodecs)

	// parsed.GetCodecForPayloadType()

	rtcOptions.Codecs = allowedCodecs
	pub := transport.NewWebRTCTransport(mid, rtcOptions)
	if pub == nil {
		return nil, nil, errors.New("publish->connect: transport.NewWebRTCTransport failed")
	}

	router := rtc.AddRouter(mid)

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(payload.Connect.Sdp)}
	answer, err := pub.Answer(offer, rtcOptions)
	if err != nil {
		log.Debugf("publish->connect: error creating answer. err=%v answer=%v", err, answer)
		return nil, nil, err
	}

	log.Debugf("publish->connect: answer => %v", answer)

	router.AddPub(pub)

	return pub, &pb.PublishReply_Connect{
		Connect: &pb.Connect{
			Sdp: []byte(answer.SDP),
		},
	}, nil
}
