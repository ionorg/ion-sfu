package sfu

import (
	"errors"
	"fmt"
	"sync"

	log "github.com/pion/ion-log"
	"github.com/pion/webrtc/v3"
)

var (
	// ErrTransportExists join is called after a peerconnection is established
	ErrTransportExists = errors.New("rtc transport already exists for this connection")
	// ErrNoTransportEstablished cannot signal before join
	ErrNoTransportEstablished = errors.New("no rtc transport exists for this Peer")
	// ErrOfferIgnored if offer received in unstable state
	ErrOfferIgnored = errors.New("offered ignored")
)

// TransportProvider provides the peerConnection to the sfu.Peer{}
// This allows the sfu.SFU{} implementation to be customized / wrapped by another package
type TransportProvider interface {
	NewWebRTCTransport(sid string, me MediaEngine) (*WebRTCTransport, error)
}

// Peer represents a single peer signal session
type Peer struct {
	sync.Mutex
	provider TransportProvider
	pc       *WebRTCTransport

	OnIceCandidate func(*webrtc.ICECandidateInit)
	OnOffer        func(*webrtc.SessionDescription)

	remoteAnswerPending bool
	negotiationPending  bool
}

// NewPeer creates a new Peer for signaling with the given SFU
func NewPeer(provider TransportProvider) Peer {
	return Peer{
		provider: provider,
	}
}

// Join initializes this peer for a given sessionID (takes an SDPOffer)
func (p *Peer) Join(sid string, sdp webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if p.pc != nil {
		log.Debugf("peer already exists")
		return nil, ErrTransportExists
	}
	p.Lock()
	defer p.Unlock()

	me := MediaEngine{}
	err := me.PopulateFromSDP(sdp)
	if err != nil {
		return nil, fmt.Errorf("error parsing sdp: %v", err)
	}

	pc, err := p.provider.NewWebRTCTransport(sid, me)
	if err != nil {
		return nil, fmt.Errorf("error creating transport: %v", err)
	}
	log.Infof("peer %s join session %s", pc.ID(), sid)
	p.pc = pc

	if err := p.pc.SetRemoteDescription(sdp); err != nil {
		return nil, fmt.Errorf("error setting remote description: %v", err)
	}

	answer, err := p.pc.CreateAnswer()
	if err != nil {
		return nil, fmt.Errorf("error creating answer: %v", err)
	}

	err = p.pc.SetLocalDescription(answer)
	if err != nil {
		return nil, fmt.Errorf("error setting local description: %v", err)
	}
	log.Infof("peer %s send answer", p.pc.ID())

	pc.OnNegotiationNeeded(func() {
		p.Lock()
		defer p.Unlock()

		if p.remoteAnswerPending {
			p.negotiationPending = true
			return
		}

		log.Debugf("peer %s negotiation needed", p.pc.ID())
		offer, err := pc.CreateOffer()
		if err != nil {
			log.Errorf("CreateOffer error: %v", err)
			return
		}

		err = pc.SetLocalDescription(offer)
		if err != nil {
			log.Errorf("SetLocalDescription error: %v", err)
			return
		}

		p.remoteAnswerPending = true
		if p.OnOffer != nil {
			log.Infof("peer %s send offer", p.pc.ID())
			p.OnOffer(&offer)
		}
	})

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		log.Debugf("on ice candidate called")
		if c == nil {
			return
		}

		if p.OnIceCandidate != nil {
			json := c.ToJSON()
			p.OnIceCandidate(&json)
		}
	})

	return &answer, nil
}

// Answer an offer from remote
func (p *Peer) Answer(sdp webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if p.pc == nil {
		return nil, ErrNoTransportEstablished
	}
	p.Lock()
	defer p.Unlock()
	log.Infof("peer %s got offer", p.pc.ID())

	readyForOffer := p.pc.SignalingState() == webrtc.SignalingStateStable && !p.remoteAnswerPending

	if !readyForOffer {
		return nil, ErrOfferIgnored
	}

	if err := p.pc.SetRemoteDescription(sdp); err != nil {
		return nil, fmt.Errorf("error setting remote description: %v", err)
	}

	answer, err := p.pc.CreateAnswer()
	if err != nil {
		return nil, fmt.Errorf("error creating answer: %v", err)
	}

	err = p.pc.SetLocalDescription(answer)
	if err != nil {
		return nil, fmt.Errorf("error setting local description: %v", err)
	}
	log.Infof("peer %s send answer", p.pc.ID())

	return &answer, nil
}

// SetRemoteDescription when receiving an answer from remote
func (p *Peer) SetRemoteDescription(sdp webrtc.SessionDescription) error {
	if p.pc == nil {
		return ErrNoTransportEstablished
	}
	p.Lock()
	defer p.Unlock()

	log.Infof("peer %s got answer", p.pc.ID())
	if err := p.pc.SetRemoteDescription(sdp); err != nil {
		return fmt.Errorf("error setting remote description: %v", err)
	}

	p.remoteAnswerPending = false

	if p.negotiationPending {
		p.negotiationPending = false
		go p.pc.negotiate()
	}

	return nil
}

// Trickle candidates available for this peer
func (p *Peer) Trickle(candidate webrtc.ICECandidateInit) error {
	if p.pc == nil {
		return ErrNoTransportEstablished
	}
	log.Infof("peer %s trickle", p.pc.ID())

	if err := p.pc.AddICECandidate(candidate); err != nil {
		return fmt.Errorf("error setting ice candidate: %s", err)
	}
	return nil
}

// Close shuts down the peer connection and sends true to the done channel
func (p *Peer) Close() error {
	log.Debugf("peer closing")
	if p.pc != nil {
		if err := p.pc.Close(); err != nil {
			return err
		}
	}
	return nil
}
