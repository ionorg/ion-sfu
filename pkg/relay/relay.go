package relay

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtcp"

	"github.com/pion/ice/v2"

	"github.com/go-logr/logr"
	"github.com/pion/webrtc/v3"
)

type Provider struct {
	mu            sync.RWMutex
	se            webrtc.SettingEngine
	log           logr.Logger
	peers         map[string]*Peer
	signal        func(meta SignalMeta, signal []byte) ([]byte, error)
	onRemote      func(meta SignalMeta, receiver *webrtc.RTPReceiver, codec *webrtc.RTPCodecParameters)
	iceServers    []webrtc.ICEServer
	onDatachannel func(meta SignalMeta, dc *webrtc.DataChannel)
}

type Signal struct {
	Metadata         SignalMeta                  `json:"metadata"`
	Encodings        *webrtc.RTPCodingParameters `json:"encodings,omitempty"`
	ICECandidates    []webrtc.ICECandidate       `json:"iceCandidates,omitempty"`
	ICEParameters    webrtc.ICEParameters        `json:"iceParameters,omitempty"`
	DTLSParameters   webrtc.DTLSParameters       `json:"dtlsParameters,omitempty"`
	CodecParameters  *webrtc.RTPCodecParameters  `json:"codecParameters,omitempty"`
	SCTPCapabilities *webrtc.SCTPCapabilities    `json:"sctpCapabilities,omitempty"`
}

type SignalMeta struct {
	PeerID    string `json:"peerId"`
	StreamID  string `json:"streamId"`
	SessionID string `json:"sessionId"`
}

type Peer struct {
	sync.Mutex
	me           *webrtc.MediaEngine
	id           string
	pid          string
	sid          string
	api          *webrtc.API
	ice          *webrtc.ICETransport
	sctp         *webrtc.SCTPTransport
	dtls         *webrtc.DTLSTransport
	provider     *Provider
	gatherer     *webrtc.ICEGatherer
	localTracks  []webrtc.TrackLocal
	dataChannels []string
}

func New(iceServers []webrtc.ICEServer, logger logr.Logger) *Provider {
	return &Provider{
		log:        logger,
		peers:      make(map[string]*Peer),
		iceServers: iceServers,
	}
}

func (p *Provider) SetSettingEngine(se webrtc.SettingEngine) {
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	p.se = se
}

func (p *Provider) SetSignaler(signaler func(meta SignalMeta, signal []byte) ([]byte, error)) {
	p.signal = signaler
}

func (p *Provider) OnRemoteStream(fn func(meta SignalMeta, receiver *webrtc.RTPReceiver, codec *webrtc.RTPCodecParameters)) {
	p.onRemote = fn
}

func (p *Provider) OnDatachannel(fn func(meta SignalMeta, dc *webrtc.DataChannel)) {
	p.onDatachannel = fn
}

func (p *Provider) AddDataChannels(sessionID, peerID string, labels []string) error {
	var r *Peer
	var err error
	p.mu.RLock()
	r = p.peers[peerID]
	p.mu.RUnlock()
	if r == nil {
		r, err = p.newRelay(sessionID, peerID)
		if err != nil {
			return err
		}
	}
	if r.ice.State() != webrtc.ICETransportStateNew {
		r.dataChannels = labels
		return r.startDataChannels()
	}
	r.dataChannels = labels
	return nil
}

func (p *Provider) Send(sessionID, peerID string, receiver *webrtc.RTPReceiver, remoteTrack *webrtc.TrackRemote,
	localTrack webrtc.TrackLocal) (*Peer, *webrtc.RTPSender, error) {
	p.mu.RLock()
	if r, ok := p.peers[peerID]; ok {
		p.mu.RUnlock()
		s, err := r.send(receiver, remoteTrack, localTrack)
		return r, s, err
	}
	p.mu.RUnlock()

	r, err := p.newRelay(sessionID, peerID)
	if err != nil {
		return nil, nil, err
	}

	s, err := r.send(receiver, remoteTrack, localTrack)
	return r, s, err
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

func (p *Provider) newRelay(sessionID, peerID string) (*Peer, error) {
	// Prepare ICE gathering options
	iceOptions := webrtc.ICEGatherOptions{
		ICEServers: p.iceServers,
	}
	me := webrtc.MediaEngine{}
	// Create an API object
	api := webrtc.NewAPI(webrtc.WithMediaEngine(&me), webrtc.WithSettingEngine(p.se))
	// Create the ICE gatherer
	gatherer, err := api.NewICEGatherer(iceOptions)
	if err != nil {
		return nil, err
	}
	// Construct the ICE transport
	i := api.NewICETransport(gatherer)
	// Construct the DTLS transport
	dtls, err := api.NewDTLSTransport(i, nil)
	// Construct the SCTP transport
	sctp := api.NewSCTPTransport(dtls)
	if err != nil {
		return nil, err
	}
	r := &Peer{
		me:       &me,
		pid:      peerID,
		sid:      sessionID,
		api:      api,
		ice:      i,
		sctp:     sctp,
		dtls:     dtls,
		provider: p,
		gatherer: gatherer,
	}

	p.mu.Lock()
	p.peers[peerID] = r
	p.mu.Unlock()

	if p.onDatachannel != nil {
		sctp.OnDataChannel(
			func(channel *webrtc.DataChannel) {
				p.onDatachannel(SignalMeta{
					PeerID:    peerID,
					StreamID:  r.id,
					SessionID: sessionID,
				}, channel)
			})
	}

	i.OnConnectionStateChange(func(state webrtc.ICETransportState) {
		if state == webrtc.ICETransportStateFailed || state == webrtc.ICETransportStateDisconnected {
			p.mu.Lock()
			delete(p.peers, peerID)
			p.mu.Unlock()
			if err := r.gatherer.Close(); err != nil {
				p.log.Error(err, "Error closing ice gatherer", "peer_id", r.pid)
			}
			if err := r.ice.Stop(); err != nil {
				p.log.Error(err, "Error stopping ice transport", "peer_id", r.pid)
			}
			if err := r.dtls.Stop(); err != nil {
				p.log.Error(err, "Error stopping dtls transport", "peer_id", r.pid)
			}
		}
	})

	return r, nil
}

func (r *Peer) WriteRTCP(pkts []rtcp.Packet) error {
	_, err := r.dtls.WriteRTCP(pkts)
	return err
}

func (r *Peer) LocalTracks() []webrtc.TrackLocal {
	return r.localTracks
}

func (r *Peer) Close() error {
	return joinErrs(r.sctp.Stop(), r.dtls.Stop(), r.ice.Stop())
}

func (r *Peer) startDataChannels() error {
	if len(r.dataChannels) == 0 {
		return nil
	}
	for idx, label := range r.dataChannels {
		id := uint16(idx)
		dcParams := &webrtc.DataChannelParameters{
			Label: label,
			ID:    &id,
		}
		channel, err := r.api.NewDataChannel(r.sctp, dcParams)
		if err != nil {
			return err
		}
		if r.provider.onDatachannel != nil {
			r.provider.onDatachannel(SignalMeta{
				PeerID:    r.pid,
				StreamID:  r.id,
				SessionID: r.sid,
			}, channel)
		}
	}
	return nil
}

func (r *Peer) receive(s Signal) ([]byte, error) {
	r.Lock()
	defer r.Unlock()

	localSignal := Signal{}
	if r.gatherer.State() == webrtc.ICEGathererStateNew {
		r.id = s.Metadata.StreamID
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

		var err error

		localSignal.ICECandidates, err = r.gatherer.GetLocalCandidates()
		if err != nil {
			return nil, err
		}

		localSignal.ICEParameters, err = r.gatherer.GetLocalParameters()
		if err != nil {
			return nil, err
		}

		localSignal.DTLSParameters, err = r.dtls.GetLocalParameters()
		if err != nil {
			return nil, err
		}

		sc := r.sctp.GetCapabilities()
		localSignal.SCTPCapabilities = &sc

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

	recv, err := r.api.NewRTPReceiver(k, r.dtls)
	if err != nil {
		return nil, err
	}

	if r.ice.State() == webrtc.ICETransportStateNew {
		go func() {
			iceRole := webrtc.ICERoleControlled

			if err = r.ice.SetRemoteCandidates(s.ICECandidates); err != nil {
				r.provider.log.Error(err, "Start ICE error")
				return
			}

			if err = r.ice.Start(r.gatherer, s.ICEParameters, &iceRole); err != nil {
				r.provider.log.Error(err, "Start ICE error")
				return
			}

			if err = r.dtls.Start(s.DTLSParameters); err != nil {
				r.provider.log.Error(err, "Start DTLS error")
				return
			}

			if s.SCTPCapabilities != nil {
				if err = r.sctp.Start(*s.SCTPCapabilities); err != nil {
					r.provider.log.Error(err, "Start SCTP error")
					return
				}
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
				}, recv, s.CodecParameters)
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
			}, recv, s.CodecParameters)
		}
	}

	b, err := json.Marshal(localSignal)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func (r *Peer) send(receiver *webrtc.RTPReceiver, remoteTrack *webrtc.TrackRemote,
	localTrack webrtc.TrackLocal) (*webrtc.RTPSender, error) {
	r.Lock()
	defer r.Unlock()

	signal := &Signal{}
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

		var err error

		signal.ICECandidates, err = r.gatherer.GetLocalCandidates()
		if err != nil {
			return nil, err
		}

		signal.ICEParameters, err = r.gatherer.GetLocalParameters()
		if err != nil {
			return nil, err
		}

		signal.DTLSParameters, err = r.dtls.GetLocalParameters()
		if err != nil {
			return nil, err
		}

		sc := r.sctp.GetCapabilities()
		signal.SCTPCapabilities = &sc

	}
	codec := remoteTrack.Codec()
	sdr, err := r.api.NewRTPSender(localTrack, r.dtls)
	r.id = remoteTrack.StreamID()
	if err != nil {
		return nil, err
	}
	if err = r.me.RegisterCodec(codec, remoteTrack.Kind()); err != nil {
		return nil, err
	}

	signal.Metadata = SignalMeta{
		PeerID:    r.pid,
		StreamID:  r.id,
		SessionID: r.sid,
	}
	signal.CodecParameters = &codec

	rr := rand.New(rand.NewSource(time.Now().UnixNano()))
	signal.Encodings = &webrtc.RTPCodingParameters{
		SSRC:        webrtc.SSRC(rr.Uint32()),
		PayloadType: remoteTrack.PayloadType(),
	}

	local, err := json.Marshal(signal)
	if err != nil {
		return nil, err
	}

	remote, err := r.provider.signal(SignalMeta{
		PeerID:    r.pid,
		StreamID:  r.id,
		SessionID: r.sid,
	}, local)
	if err != nil {
		return nil, err
	}
	var remoteSignal Signal
	if err = json.Unmarshal(remote, &remoteSignal); err != nil {
		return nil, err
	}

	if r.ice.State() == webrtc.ICETransportStateNew {
		if err = r.ice.SetRemoteCandidates(remoteSignal.ICECandidates); err != nil {
			return nil, err
		}
		iceRole := webrtc.ICERoleControlling
		if err = r.ice.Start(r.gatherer, remoteSignal.ICEParameters, &iceRole); err != nil {
			return nil, err
		}

		if err = r.dtls.Start(remoteSignal.DTLSParameters); err != nil {
			return nil, err
		}

		if remoteSignal.SCTPCapabilities != nil {
			if err = r.sctp.Start(*remoteSignal.SCTPCapabilities); err != nil {
				return nil, err
			}
		}

		if err = r.startDataChannels(); err != nil {
			return nil, err
		}
	}
	params := receiver.GetParameters()

	if err = sdr.Send(webrtc.RTPSendParameters{
		RTPParameters: params,
		Encodings: []webrtc.RTPEncodingParameters{
			{
				webrtc.RTPCodingParameters{
					SSRC:        signal.Encodings.SSRC,
					PayloadType: signal.Encodings.PayloadType,
					RID:         remoteTrack.RID(),
				},
			},
		},
	}); err != nil {
		return nil, err
	}
	r.localTracks = append(r.localTracks, localTrack)
	return sdr, nil
}

func joinErrs(errs ...error) error {
	var joinErrsR func(string, int, ...error) error
	joinErrsR = func(soFar string, count int, errs ...error) error {
		if len(errs) == 0 {
			if count == 0 {
				return nil
			}
			return fmt.Errorf(soFar)
		}
		current := errs[0]
		next := errs[1:]
		if current == nil {
			return joinErrsR(soFar, count, next...)
		}
		count++
		if count == 1 {
			return joinErrsR(fmt.Sprintf("%s", current), count, next...)
		} else if count == 2 {
			return joinErrsR(fmt.Sprintf("1: %s\n2: %s", soFar, current), count, next...)
		}
		return joinErrsR(fmt.Sprintf("%s\n%d: %s", soFar, count, current), count, next...)
	}
	return joinErrsR("", 0, errs...)
}
