package relay

import (
	"encoding/json"
	"strings"

	"github.com/pion/webrtc/v3"
)

type Signal struct {
	StreamID         string                      `json:"streamId"`
	ICECandidates    []webrtc.ICECandidate       `json:"iceCandidates,omitempty"`
	ICEParameters    webrtc.ICEParameters        `json:"iceParameters,omitempty"`
	DTLSParameters   webrtc.DTLSParameters       `json:"dtlsParameters,omitempty"`
	SCTPCapabilities *webrtc.SCTPCapabilities    `json:"sctpCapabilities,omitempty"`
	CodecParameters  *webrtc.RTPCodecParameters  `json:"codecParameters,omitempty"`
	Encodings        *webrtc.RTPCodingParameters `json:"encodings,omitempty"`
}

type Relay struct {
	api      *webrtc.API
	ice      *webrtc.ICETransport
	dtls     *webrtc.DTLSTransport
	peers    map[string]*RelayedStreams
	gatherer *webrtc.ICEGatherer
}

type RelayedStreams struct {
	sctp     *webrtc.SCTPTransport
	sender   *webrtc.RTPSender
	receiver *webrtc.RTPReceiver
}

func New() (*Relay, error) {
	// Prepare ICE gathering options
	iceOptions := webrtc.ICEGatherOptions{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}
	// Create an API object
	api := webrtc.NewAPI()
	// Create the ICE gatherer
	gatherer, err := api.NewICEGatherer(iceOptions)
	if err != nil {
		return nil, err
	}
	// Construct the ICE transport
	ice := api.NewICETransport(gatherer)
	// Construct the DTLS transport
	dtls, err := api.NewDTLSTransport(ice, nil)
	if err != nil {
		return nil, err
	}
	return &Relay{
		api:      api,
		ice:      ice,
		dtls:     dtls,
		peers:    make(map[string]*RelayedStreams, 4),
		gatherer: gatherer,
	}, nil
}

func (r *Relay) Receive(signal []byte) (ReceiveStream, error) {
	var s Signal
	if err := json.Unmarshal(signal, &s); err != nil {
		return nil, err
	}
	var k webrtc.RTPCodecType
	switch {
	case strings.HasPrefix(s.CodecParameters.MimeType, "audio/"):
		k = webrtc.RTPCodecTypeAudio
	case strings.HasPrefix(s.CodecParameters.MimeType, "video/"):
		k = webrtc.RTPCodecTypeVideo
	default:
		k = webrtc.RTPCodecType(0)
	}
	recv, err := r.api.NewRTPReceiver(k, r.dtls)
	if err != nil {
		return nil, err
	}

	gatherFinished := make(chan struct{})
	r.gatherer.OnLocalCandidate(func(i *webrtc.ICECandidate) {
		if i == nil {
			close(gatherFinished)
		}
	})
	// Gather candidates
	err = r.gatherer.Gather()
	if err != nil {
		return nil, err
	}

	<-gatherFinished

	iceCandidates, err := r.gatherer.GetLocalCandidates()
	if err != nil {
		return nil, err
	}

	iceParams, err := r.gatherer.GetLocalParameters()
	if err != nil {
		return nil, err
	}

	dtlsParams, err := r.dtls.GetLocalParameters()
	if err != nil {
		return nil, err
	}

	return &receiveStream{
		relay:        r,
		remoteSignal: s,
		localSignal: Signal{
			StreamID:       s.StreamID,
			ICECandidates:  iceCandidates,
			ICEParameters:  iceParams,
			DTLSParameters: dtlsParams,
		},
		receiver: recv,
	}, nil
}

func (r *Relay) Send(receiver *webrtc.RTPReceiver) (SendStream, error) {
	t := receiver.Track()
	codec := receiver.Track().Codec()
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType:     codec.MimeType,
		ClockRate:    codec.ClockRate,
		Channels:     codec.Channels,
		SDPFmtpLine:  codec.SDPFmtpLine,
		RTCPFeedback: codec.RTCPFeedback,
	}, t.ID(), t.StreamID())
	if err != nil {
		return nil, err
	}
	sdr, err := r.api.NewRTPSender(track, r.dtls)
	if err != nil {
		return nil, err
	}
	gatherFinished := make(chan struct{})
	r.gatherer.OnLocalCandidate(func(i *webrtc.ICECandidate) {
		if i == nil {
			close(gatherFinished)
		}
	})
	// Gather candidates
	err = r.gatherer.Gather()
	if err != nil {
		return nil, err
	}

	<-gatherFinished

	iceCandidates, err := r.gatherer.GetLocalCandidates()
	if err != nil {
		return nil, err
	}

	iceParams, err := r.gatherer.GetLocalParameters()
	if err != nil {
		return nil, err
	}

	dtlsParams, err := r.dtls.GetLocalParameters()
	if err != nil {
		return nil, err
	}

	return &sendStream{
		relay: r,
		signal: Signal{
			StreamID:        t.StreamID(),
			ICECandidates:   iceCandidates,
			ICEParameters:   iceParams,
			DTLSParameters:  dtlsParams,
			CodecParameters: &codec,
			Encodings: &webrtc.RTPCodingParameters{
				SSRC:        t.SSRC(),
				PayloadType: t.PayloadType(),
			},
		},
		sender: sdr,
	}, nil
}
