package sfu

import (
	"errors"
	"fmt"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/webrtc/v3"
)

var (
	errTransportExists        = errors.New("rtc transport already exists for this connection")
	errNoTransportEstablished = errors.New("no rtc transport exists for this Peer")
)

// Peer represents a single peer signal session
type Peer struct {
	sfu *SFU
	pc  *WebRTCTransport

	Done      chan bool
	IceChan   chan *webrtc.ICECandidate
	OfferChan chan *webrtc.SessionDescription
}

// NewPeer creates a new Peer for signaling with the given SFU
func NewPeer(sfu *SFU) Peer {
	return Peer{
		sfu:       sfu,
		Done:      make(chan bool),
		IceChan:   make(chan *webrtc.ICECandidate),
		OfferChan: make(chan *webrtc.SessionDescription),
	}
}

// Join initializes this peer for a given sessionID (takes an SDPOffer)
func (p *Peer) Join(sid string, sdp webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if p.pc != nil {
		return nil, errTransportExists
	}

	me := MediaEngine{}
	err := me.PopulateFromSDP(sdp)
	if err != nil {
		return nil, fmt.Errorf("error parsing sdp: %v", err)
	}

	pc, err := p.sfu.NewWebRTCTransport(sid, me)
	if err != nil {
		return nil, fmt.Errorf("error creating transport: %v", err)
	}
	log.Infof("peer %s join session %s", pc.ID(), sid)
	p.pc = pc

	answer, err := p.Offer(sdp)
	if err != nil {
		return nil, err
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

		p.OfferChan <- &offer
	})

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		log.Debugf("on ice candidate called")
		if c == nil {
			return
		}
		p.IceChan <- c
	})

	p.pc = pc
	return answer, nil
}

// Offer new offer available over signaling for this peer
func (p *Peer) Offer(sdp webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if p.pc == nil {
		return nil, errNoTransportEstablished
	}
	log.Infof("peer %s offer", p.pc.ID())

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

	return &answer, nil
}

// Answer available over signaling for this peer
func (p *Peer) Answer(sdp webrtc.SessionDescription) error {
	if p.pc == nil {
		return errNoTransportEstablished
	}
	log.Infof("peer %s answer", p.pc.ID())
	if err := p.pc.SetRemoteDescription(sdp); err != nil {
		return fmt.Errorf("error setting remote description: %v", err)
	}
	return nil
}

// Trickle candidates available for this peer
func (p *Peer) Trickle(candidate webrtc.ICECandidateInit) error {
	if p.pc == nil {
		return errNoTransportEstablished
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

	p.Done <- true
	close(p.IceChan)
	close(p.OfferChan)
	return nil
}
