package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	sfu "github.com/pion/ion-sfu/pkg"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/webrtc/v3"
	"github.com/sourcegraph/jsonrpc2"
)

var (
	errTransportExists = errors.New("transport already exists for this connection")
)

// Join message sent when initializing a peer connection
type Join struct {
	Sid   string                    `json:"sid"`
	Offer webrtc.SessionDescription `json:"offer"`
}

// Negotiation message sent when renegotiating the peer connection
type Negotiation struct {
	Desc webrtc.SessionDescription `json:"desc"`
}

// Trickle message sent when renegotiating the peer connection
type Trickle struct {
	Candidate webrtc.ICECandidateInit `json:"candidate"`
}

// peer represents a single peer signal session
type peer struct {
	sfu *sfu.SFU
	pc  *sfu.WebRTCTransport

	iceChan   chan *webrtc.ICECandidate
	offerChan chan *webrtc.SessionDescription
}

func (p *peer) Join(sid string, sdp webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if p.pc != nil {
		return nil, errTransportExists
	}

	me := sfu.MediaEngine{}
	err := me.PopulateFromSDP(sdp)
	if err != nil {
		return nil, fmt.Errorf("error parsing sdp: %v", err)
	}

	pc, err := p.sfu.NewWebRTCTransport(sid, me)
	if err != nil {
		return nil, fmt.Errorf("error creating transport: %v", err)
	}
	log.Infof("peer %s join session %s", pc.ID(), sid)

	answer, err := pc.CreateAnswer()
	if err != nil {
		log.Errorf("Offer error: answer=%v err=%v", answer, err)
		return nil, fmt.Errorf("error creating answer: %v", err)
	}

	err = pc.SetLocalDescription(answer)
	if err != nil {
		log.Errorf("Offer error: answer=%v err=%v", answer, err)
		return nil, fmt.Errorf("error setting local description: %v", err)
	}

	p.iceChan = make(chan *webrtc.ICECandidate)
	p.offerChan = make(chan *webrtc.SessionDescription)
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		p.iceChan <- c
	})
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

		p.offerChan <- &offer
	})

	p.pc = pc
	return &answer, nil
}

func (p *peer) Negotiation(sdp webrtc.SessionDescription) error {

}

func (p *peer) Trickle(candidate webrtc.ICECandidateInit) error {

}

func (p *peer) Close() error {
	log.Debugf("peer closing")
	if p.pc != nil {
		if err := p.pc.Close(); err != nil {
			return err
		}
	}
	close(p.iceChan)
	close(p.offerChan)
	return nil
}

// Handle incoming RPC call events like join, answer, offer and trickle
func (p *peer) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	switch req.Method {
	case "join":
		var join Join
		err := json.Unmarshal(*req.Params, &join)
		if err != nil {
			log.Errorf("connect: error parsing offer: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		p.Join(join.Sid, join.Offer)

	case "offer":
		fallthrough
	case "answer":
		var negotiation Negotiation
		err := json.Unmarshal(*req.Params, &negotiation)
		if err != nil {
			log.Errorf("connect: error parsing offer: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

	case "trickle":
		var trickle Trickle
		err := json.Unmarshal(*req.Params, &trickle)
		if err != nil {
			log.Errorf("connect: error parsing candidate: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}
	}
}
