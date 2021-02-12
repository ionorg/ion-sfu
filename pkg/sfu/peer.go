package sfu

import (
	"errors"
	"fmt"
	"sync"

	"github.com/lucsky/cuid"
	log "github.com/pion/ion-log"
	"github.com/pion/webrtc/v3"
)

const (
	publisher  = 0
	subscriber = 1
)

var (
	// ErrTransportExists join is called after a peerconnection is established
	ErrTransportExists = errors.New("rtc transport already exists for this connection")
	// ErrNoTransportEstablished cannot signal before join
	ErrNoTransportEstablished = errors.New("no rtc transport exists for this Peer")
	// ErrOfferIgnored if offer received in unstable state
	ErrOfferIgnored = errors.New("offered ignored")
)

// SessionProvider provides the session to the sfu.Peer{}
// This allows the sfu.SFU{} implementation to be customized / wrapped by another package
type SessionProvider interface {
	GetSession(sid string) (*Session, WebRTCTransportConfig)
}

// Peer represents a pair peer connection
type Peer struct {
	sync.Mutex
	id       string
	closed   atomicBool
	session  *Session
	provider SessionProvider

	publisher  *Publisher
	subscriber *Subscriber

	OnOffer                    func(*webrtc.SessionDescription)
	OnIceCandidate             func(*webrtc.ICECandidateInit, int)
	OnICEConnectionStateChange func(webrtc.ICEConnectionState)

	remoteAnswerPending bool
	negotiationPending  bool
}

// NewPeer creates a new Peer for signaling with the given SFU
func NewPeer(provider SessionProvider) *Peer {
	return &Peer{
		provider: provider,
	}
}

// Join initializes this peer for a given sessionID
func (p *Peer) Join(sid, uid string) error {
	if p.publisher != nil {
		log.Debugf("peer already exists")
		return ErrTransportExists
	}

	if uid == "" {
		uid = cuid.New()
	}
	p.id = uid
	var (
		cfg WebRTCTransportConfig
		err error
	)

	p.session, cfg = p.provider.GetSession(sid)

	p.subscriber, err = NewSubscriber(uid, cfg)
	if err != nil {
		return fmt.Errorf("error creating transport: %v", err)
	}
	p.publisher, err = NewPublisher(p.session, uid, cfg)
	if err != nil {
		return fmt.Errorf("error creating transport: %v", err)
	}

	for _, dc := range p.session.datachannels {
		if err := p.subscriber.AddDatachannel(p, dc); err != nil {
			return fmt.Errorf("error setting subscriber default dc datachannel")
		}
	}

	p.subscriber.OnNegotiationNeeded(func() {
		p.Lock()
		defer p.Unlock()

		if p.remoteAnswerPending {
			p.negotiationPending = true
			return
		}

		log.Debugf("peer %s negotiation needed", p.id)
		offer, err := p.subscriber.CreateOffer()
		if err != nil {
			log.Errorf("CreateOffer error: %v", err)
			return
		}

		p.remoteAnswerPending = true
		if p.OnOffer != nil && !p.closed.get() {
			log.Infof("peer %s send offer", p.id)
			p.OnOffer(&offer)
		}
	})

	p.subscriber.OnICECandidate(func(c *webrtc.ICECandidate) {
		log.Debugf("on subscriber ice candidate called for peer " + p.id)
		if c == nil {
			return
		}

		if p.OnIceCandidate != nil && !p.closed.get() {
			json := c.ToJSON()
			p.OnIceCandidate(&json, subscriber)
		}
	})

	p.publisher.OnICECandidate(func(c *webrtc.ICECandidate) {
		log.Debugf("on publisher ice candidate called for peer " + p.id)
		if c == nil {
			return
		}

		if p.OnIceCandidate != nil && !p.closed.get() {
			json := c.ToJSON()
			p.OnIceCandidate(&json, publisher)
		}
	})

	p.publisher.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		if p.OnICEConnectionStateChange != nil && !p.closed.get() {
			p.OnICEConnectionStateChange(s)
		}
	})

	p.session.AddPeer(p)

	log.Infof("peer %s join session %s", p.id, sid)

	p.session.Subscribe(p)

	return nil
}

// Answer an offer from remote
func (p *Peer) Answer(sdp webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if p.publisher == nil {
		return nil, ErrNoTransportEstablished
	}

	log.Infof("peer %s got offer", p.id)

	if p.publisher.SignalingState() != webrtc.SignalingStateStable {
		return nil, ErrOfferIgnored
	}

	answer, err := p.publisher.Answer(sdp)
	if err != nil {
		return nil, fmt.Errorf("error creating answer: %v", err)
	}

	log.Infof("peer %s send answer", p.id)

	return &answer, nil
}

// SetRemoteDescription when receiving an answer from remote
func (p *Peer) SetRemoteDescription(sdp webrtc.SessionDescription) error {
	if p.subscriber == nil {
		return ErrNoTransportEstablished
	}
	p.Lock()
	defer p.Unlock()

	log.Infof("peer %s got answer", p.id)
	if err := p.subscriber.SetRemoteDescription(sdp); err != nil {
		return fmt.Errorf("error setting remote description: %v", err)
	}

	p.remoteAnswerPending = false

	if p.negotiationPending {
		p.negotiationPending = false
		p.subscriber.negotiate()
	}

	return nil
}

// Trickle candidates available for this peer
func (p *Peer) Trickle(candidate webrtc.ICECandidateInit, target int) error {
	if p.subscriber == nil || p.publisher == nil {
		return ErrNoTransportEstablished
	}
	log.Infof("peer %s trickle", p.id)
	switch target {
	case publisher:
		if err := p.publisher.AddICECandidate(candidate); err != nil {
			return fmt.Errorf("error setting ice candidate: %s", err)
		}
	case subscriber:
		if err := p.subscriber.AddICECandidate(candidate); err != nil {
			return fmt.Errorf("error setting ice candidate: %s", err)
		}
	}
	return nil
}

// Close shuts down the peer connection and sends true to the done channel
func (p *Peer) Close() error {
	p.Lock()
	defer p.Unlock()

	p.closed.set(true)

	if p.session != nil {
		p.session.RemovePeer(p.id)
	}
	if p.publisher != nil {
		p.publisher.Close()
	}
	if p.subscriber != nil {
		if err := p.subscriber.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (p *Peer) Subscriber() *Subscriber {
	return p.subscriber
}

func (p *Peer) Publisher() *Publisher {
	return p.publisher
}

func (p *Peer) Session() *Session {
	return p.session
}

// ID return the peer id
func (p *Peer) ID() string {
	return p.id
}
