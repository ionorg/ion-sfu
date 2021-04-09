package sfu

import (
	"errors"
	"fmt"
	"sync"

	"github.com/lucsky/cuid"

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

// JoinConfig allow adding more control to the peers joining a session.
type JoinConfig struct {
	// If true the peer will not be allowed to publish tracks to session.
	NoPublish bool
	// If true the peer will not be allowed to subscribe to other peers in session.
	NoSubscribe bool
	// If true it will relay all the published tracks of the peer
	Relay bool
}

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
func (p *Peer) Join(sid, uid string, config ...JoinConfig) error {
	var conf JoinConfig
	if len(config) > 0 {
		conf = config[0]
	}

	if p.session != nil {
		Logger.V(1).Info("peer already exists", "session_id", sid, "peer_id", p.id, "publisher_id", p.publisher.id)
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

	if !conf.NoSubscribe {
		p.subscriber, err = NewSubscriber(uid, cfg)
		if err != nil {
			return fmt.Errorf("error creating transport: %v", err)
		}

		p.subscriber.OnNegotiationNeeded(func() {
			p.Lock()
			defer p.Unlock()

			if p.remoteAnswerPending {
				p.negotiationPending = true
				return
			}

			Logger.V(1).Info("Negotiation needed", "peer_id", p.id)
			offer, err := p.subscriber.CreateOffer()
			if err != nil {
				Logger.Error(err, "CreateOffer error")
				return
			}

			p.remoteAnswerPending = true
			if p.OnOffer != nil && !p.closed.get() {
				Logger.V(0).Info("Send offer", "peer_id", p.id)
				p.OnOffer(&offer)
			}
		})

		p.subscriber.OnICECandidate(func(c *webrtc.ICECandidate) {
			Logger.V(1).Info("On subscriber ice candidate called for peer", "peer_id", p.id)
			if c == nil {
				return
			}

			if p.OnIceCandidate != nil && !p.closed.get() {
				json := c.ToJSON()
				p.OnIceCandidate(&json, subscriber)
			}
		})
	}

	if !conf.NoPublish {
		p.publisher, err = NewPublisher(p.session, uid, conf.Relay, cfg)
		if err != nil {
			return fmt.Errorf("error creating transport: %v", err)
		}
		for _, dc := range p.session.datachannels {
			if err := p.subscriber.AddDatachannel(p, dc); err != nil {
				return fmt.Errorf("error setting subscriber default dc datachannel")
			}
		}

		p.publisher.OnICECandidate(func(c *webrtc.ICECandidate) {
			Logger.V(1).Info("on publisher ice candidate called for peer", "peer_id", p.id)
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
	}

	p.session.AddPeer(p)

	Logger.V(0).Info("Peer join session", "peer_id", p.id, "session_id", sid)

	if !conf.NoSubscribe {
		p.session.Subscribe(p)
	}
	return nil
}

// Answer an offer from remote
func (p *Peer) Answer(sdp webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if p.publisher == nil {
		return nil, ErrNoTransportEstablished
	}

	Logger.V(0).Info("Peer got offer", "peer_id", p.id)

	if p.publisher.SignalingState() != webrtc.SignalingStateStable {
		return nil, ErrOfferIgnored
	}

	answer, err := p.publisher.Answer(sdp)
	if err != nil {
		return nil, fmt.Errorf("error creating answer: %v", err)
	}

	Logger.V(0).Info("Peer send answer", "peer_id", p.id)

	return &answer, nil
}

// SetRemoteDescription when receiving an answer from remote
func (p *Peer) SetRemoteDescription(sdp webrtc.SessionDescription) error {
	if p.subscriber == nil {
		return ErrNoTransportEstablished
	}
	p.Lock()
	defer p.Unlock()

	Logger.V(0).Info("Peer got answer", "peer_id", p.id)
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
	Logger.V(0).Info("Peer trickle", "peer_id", p.id)
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
