package sfu

import (
	"errors"
	"fmt"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/webrtc/v3"
)

var (
	// ErrTransportExists join is called after a peerconnection is established
	ErrTransportExists = errors.New("rtc transport already exists for this connection")
	// ErrNoTransportEstablished cannot signal before join
	ErrNoTransportEstablished = errors.New("no rtc transport exists for this Peer")
)

// Peer represents a single peer signal session
type Peer struct {
	sfu *SFU
	pc  *WebRTCTransport

	OnICECandidate func(*webrtc.ICECandidateInit)
	OnOffer        func(*webrtc.SessionDescription)
}

// NewPeer creates a new Peer for signaling with the given SFU
func NewPeer(sfu *SFU) Peer {
	return Peer{
		sfu: sfu,
	}
}

// Join initializes this peer for a given session and offer sdp.
// It returns an answer for the given offer and an offer which could
// contain media from existing peers in the session
func (p *Peer) Join(sid string, sdp webrtc.SessionDescription) (*webrtc.SessionDescription, *webrtc.SessionDescription, error) {
	if p.pc != nil {
		log.Debugf("peer already exists")
		return nil, nil, ErrTransportExists
	}

	me := MediaEngine{}
	err := me.PopulateFromSDP(sdp)
	if err != nil {
		return nil, nil, fmt.Errorf("error parsing sdp: %v", err)
	}

	pc, err := p.sfu.NewWebRTCTransport(sid, me)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating transport: %v", err)
	}
	log.Debugf("peer %s join session %s", pc.ID(), sid)
	p.pc = pc

	// Get answer for pub
	answer, err := p.Answer(sdp)
	if err != nil {
		return nil, nil, err
	}

	// Subscribe to peers in session
	pc.Subscribe()

	offer, err := pc.CreateOffer()
	if err != nil {
		return nil, nil, err
	}

	pc.OnNegotiationNeeded(func() {
		log.Debugf("on negotiation needed called")
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

		if p.OnOffer != nil {
			p.OnOffer(&offer)
		}
	})

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		log.Debugf("on ice candidate called")
		if c == nil {
			return
		}

		if p.OnICECandidate != nil {
			json := c.ToJSON()
			p.OnICECandidate(&json)
		}
	})

	p.pc = pc
	return answer, &offer, nil
}

// Answer an offer from the remove peer connection
func (p *Peer) Answer(offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if p.pc == nil {
		return nil, ErrNoTransportEstablished
	}
	log.Debugf("peer %s offer", p.pc.ID())

	if err := p.pc.SetRemoteDescription(offer); err != nil {
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

	return &answer, nil
}

// Answer available over signaling for this peer
func (p *Peer) SetRemoteDescription(answer webrtc.SessionDescription) error {
	if p.pc == nil {
		return ErrNoTransportEstablished
	}
	log.Debugf("peer %s answer", p.pc.ID())
	if err := p.pc.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("error setting remote description: %v", err)
	}
	return nil
}

// Trickle candidates available for this peer
func (p *Peer) Trickle(candidate webrtc.ICECandidateInit) error {
	if p.pc == nil {
		return ErrNoTransportEstablished
	}
	log.Debugf("peer %s trickle", p.pc.ID())

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
