package relay

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"github.com/pion/webrtc/v3"
)

type Provider struct {
	mu       sync.RWMutex
	log      logr.Logger
	signal   func(meta SignalMeta, signal []byte) ([]byte, error)
	peers    map[string]*relayPeer
	onRemote func(meta SignalMeta, receiver *webrtc.RTPReceiver)
}

type Signal struct {
	Metadata         SignalMeta                  `json:"metadata"`
	ICECandidates    []webrtc.ICECandidate       `json:"iceCandidates,omitempty"`
	ICEParameters    webrtc.ICEParameters        `json:"iceParameters,omitempty"`
	DTLSParameters   webrtc.DTLSParameters       `json:"dtlsParameters,omitempty"`
	SCTPCapabilities *webrtc.SCTPCapabilities    `json:"sctpCapabilities,omitempty"`
	CodecParameters  *webrtc.RTPCodecParameters  `json:"codecParameters,omitempty"`
	Encodings        *webrtc.RTPCodingParameters `json:"encodings,omitempty"`
}

type SignalMeta struct {
	PeerID    string `json:"peerId"`
	StreamID  string `json:"streamId"`
	SessionID string `json:"sessionId"`
}

type relayPeer struct {
	me       *webrtc.MediaEngine
	pid      string
	sid      string
	api      *webrtc.API
	ice      *webrtc.ICETransport
	dtls     *webrtc.DTLSTransport
	provider *Provider
	gatherer *webrtc.ICEGatherer
}

type RelayedStreams struct {
	sctp     *webrtc.SCTPTransport
	sender   *webrtc.RTPSender
	receiver *webrtc.RTPReceiver
}

func New(logger logr.Logger) *Provider {
	return &Provider{
		log:   logger,
		peers: make(map[string]*relayPeer),
	}
}

func (p *Provider) SetSignaler(signaler func(meta SignalMeta, signal []byte) ([]byte, error)) {
	p.signal = signaler
}

func (p *Provider) OnRemoteStream(fn func(meta SignalMeta, receiver *webrtc.RTPReceiver)) {
	p.onRemote = fn
}

func (p *Provider) Send(sessionID, peerID string, receiver *webrtc.RTPReceiver, localTrack webrtc.TrackLocal) error {
	p.mu.RLock()
	if r, ok := p.peers[peerID]; ok {
		p.mu.RUnlock()
		return r.send(receiver, localTrack)
	}
	p.mu.RUnlock()

	r, err := p.newRelay(sessionID, peerID)
	if err != nil {
		return err
	}

	return r.send(receiver, localTrack)
}

func (p *Provider) Receive(remoteSignal []byte) ([]byte, error) {
	s := Signal{}
	if err := json.Unmarshal(remoteSignal, &s); err != nil {
		return nil, err
	}

	p.mu.RLock()
	if r, ok := p.peers[s.Metadata.PeerID]; ok {
		p.mu.RUnlock()
		return r.receive(s)
	}
	p.mu.RUnlock()

	r, err := p.newRelay(s.Metadata.SessionID, s.Metadata.PeerID)
	if err != nil {
		return nil, err
	}

	return r.receive(s)
}

func (p *Provider) newRelay(sessionID, peerID string) (*relayPeer, error) {
	// Prepare ICE gathering options
	iceOptions := webrtc.ICEGatherOptions{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}
	me := webrtc.MediaEngine{}
	// Create an API object
	api := webrtc.NewAPI(webrtc.WithMediaEngine(&me))
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
	r := &relayPeer{
		me:       &me,
		pid:      peerID,
		sid:      sessionID,
		api:      api,
		ice:      ice,
		dtls:     dtls,
		provider: p,
		gatherer: gatherer,
	}

	p.mu.Lock()
	p.peers[peerID] = r
	p.mu.Unlock()

	ice.OnConnectionStateChange(func(state webrtc.ICETransportState) {
		if state == webrtc.ICETransportStateFailed || state == webrtc.ICETransportStateDisconnected {
			p.mu.Lock()
			delete(p.peers, peerID)
			p.mu.Unlock()
			r.gatherer.Close()
			r.ice.Stop()
			r.dtls.Stop()
		}
	})

	return r, nil
}

func (r *relayPeer) receive(s Signal) ([]byte, error) {
	if r.gatherer.State() == webrtc.ICEGathererStateNew {
		gatherFinished := make(chan struct{})
		r.gatherer.OnLocalCandidate(func(i *webrtc.ICECandidate) {
			if i == nil {
				close(gatherFinished)
			}
		})
		// Gather candidates
		if err := r.gatherer.Gather(); err != nil {
			return nil, err
		}
		<-gatherFinished
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
	if err := r.me.RegisterCodec(*s.CodecParameters, k); err != nil {
		return nil, err
	}

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

	localSignal := Signal{
		ICECandidates:  iceCandidates,
		ICEParameters:  iceParams,
		DTLSParameters: dtlsParams,
	}

	if err = r.ice.SetRemoteCandidates(s.ICECandidates); err != nil {
		return nil, err
	}

	recv, err := r.api.NewRTPReceiver(k, r.dtls)
	if err != nil {
		return nil, err
	}

	if r.ice.State() == webrtc.ICETransportStateNew {
		go func() {
			iceRole := webrtc.ICERoleControlled
			if err = r.ice.Start(nil, s.ICEParameters, &iceRole); err != nil {
				r.provider.log.Error(err, "Start ICE error")
				return
			}

			if err = r.dtls.Start(s.DTLSParameters); err != nil {
				r.provider.log.Error(err, "Start DTLS error")
				return
			}

			if err = recv.Receive(webrtc.RTPReceiveParameters{Encodings: []webrtc.RTPDecodingParameters{
				{
					webrtc.RTPCodingParameters{
						RID:         s.Encodings.RID,
						SSRC:        s.Encodings.SSRC,
						PayloadType: s.Encodings.PayloadType,
					},
				},
			}}); err != nil {
				r.provider.log.Error(err, "Start receiver error")
				return
			}

			if r.provider.onRemote != nil {
				r.provider.onRemote(SignalMeta{
					PeerID:    s.Metadata.PeerID,
					StreamID:  s.Metadata.StreamID,
					SessionID: s.Metadata.SessionID,
				}, recv)
			}
		}()
	} else {
		if err = recv.Receive(webrtc.RTPReceiveParameters{Encodings: []webrtc.RTPDecodingParameters{
			{
				webrtc.RTPCodingParameters{
					RID:         s.Encodings.RID,
					SSRC:        s.Encodings.SSRC,
					PayloadType: s.Encodings.PayloadType,
				},
			},
		}}); err != nil {
			return nil, err
		}

		if r.provider.onRemote != nil {
			r.provider.onRemote(SignalMeta{
				PeerID:    s.Metadata.PeerID,
				StreamID:  s.Metadata.StreamID,
				SessionID: s.Metadata.SessionID,
			}, recv)
		}
	}

	b, err := json.Marshal(localSignal)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func (r *relayPeer) send(receiver *webrtc.RTPReceiver, localTrack webrtc.TrackLocal) error {
	if r.gatherer.State() == webrtc.ICEGathererStateNew {
		gatherFinished := make(chan struct{})
		r.gatherer.OnLocalCandidate(func(i *webrtc.ICECandidate) {
			if i == nil {
				close(gatherFinished)
			}
		})
		// Gather candidates
		if err := r.gatherer.Gather(); err != nil {
			return err
		}
		<-gatherFinished
	}
	t := receiver.Track()
	codec := receiver.Track().Codec()
	sdr, err := r.api.NewRTPSender(localTrack, r.dtls)
	if err != nil {
		return err
	}
	if err = r.me.RegisterCodec(codec, t.Kind()); err != nil {
		return err
	}

	iceCandidates, err := r.gatherer.GetLocalCandidates()
	if err != nil {
		return err
	}

	iceParams, err := r.gatherer.GetLocalParameters()
	if err != nil {
		return err
	}

	dtlsParams, err := r.dtls.GetLocalParameters()
	if err != nil {
		return err
	}

	signal := &Signal{
		ICECandidates:   iceCandidates,
		ICEParameters:   iceParams,
		DTLSParameters:  dtlsParams,
		CodecParameters: &codec,
		Encodings: &webrtc.RTPCodingParameters{
			SSRC:        t.SSRC(),
			PayloadType: t.PayloadType(),
		},
	}
	local, err := json.Marshal(signal)
	if err != nil {
		return err
	}

	remote, err := r.provider.signal(SignalMeta{
		PeerID:    r.pid,
		StreamID:  t.StreamID(),
		SessionID: r.sid,
	}, local)
	if err != nil {
		return err
	}
	var remoteSignal Signal
	if err = json.Unmarshal(remote, &remoteSignal); err != nil {
		return err
	}

	if err = r.ice.SetRemoteCandidates(remoteSignal.ICECandidates); err != nil {
		return err
	}

	if r.ice.State() == webrtc.ICETransportStateNew {
		iceRole := webrtc.ICERoleControlling
		if err = r.ice.Start(nil, remoteSignal.ICEParameters, &iceRole); err != nil {
			return err
		}

		if err = r.dtls.Start(remoteSignal.DTLSParameters); err != nil {
			return err
		}
	}
	params := receiver.GetParameters()

	if err = sdr.Send(webrtc.RTPSendParameters{
		RTPParameters: params,
		Encodings: []webrtc.RTPEncodingParameters{
			{
				webrtc.RTPCodingParameters{
					SSRC:        t.SSRC(),
					PayloadType: t.PayloadType(),
					RID:         t.RID(),
				},
			},
		},
	}); err != nil {
		return err
	}
	return nil
}
