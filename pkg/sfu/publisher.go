package sfu

import (
	"sync"
	"sync/atomic"

	log "github.com/pion/ion-log"
	"github.com/pion/webrtc/v3"
)

type Publisher struct {
	id string
	pc *webrtc.PeerConnection

	router     Router
	session    *Session
	candidates []webrtc.ICECandidateInit

	onTrackHandler                    func(*webrtc.TrackRemote, *webrtc.RTPReceiver)
	onICEConnectionStateChangeHandler atomic.Value // func(webrtc.ICEConnectionState)

	closeOnce sync.Once
}

// NewPublisher creates a new Publisher
func NewPublisher(session *Session, id string, cfg WebRTCTransportConfig) (*Publisher, error) {
	me, err := getPublisherMediaEngine()
	if err != nil {
		log.Errorf("NewPeer error: %v", err)
		return nil, errPeerConnectionInitFailed
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(cfg.setting))
	pc, err := api.NewPeerConnection(cfg.configuration)

	if err != nil {
		log.Errorf("NewPeer error: %v", err)
		return nil, errPeerConnectionInitFailed
	}

	p := &Publisher{
		id:      id,
		pc:      pc,
		session: session,
		router:  newRouter(pc, id, cfg.router),
	}

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Debugf("Peer %s got remote track id: %s mediaSSRC: %d rid :%s streamID: %s", p.id, track.ID(), track.SSRC(), track.RID(), track.StreamID())
		if r, pub := p.router.AddReceiver(receiver, track); pub {
			p.session.Publish(p.router, r)
		}
	})

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() == apiChannelLabel {
			// terminate api data channel
			return
		}
		p.session.AddDatachannel(id, dc)
	})

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Debugf("ice connection state: %s", connectionState)
		switch connectionState {
		case webrtc.ICEConnectionStateFailed:
			fallthrough
		case webrtc.ICEConnectionStateClosed:
			log.Debugf("webrtc ice closed for peer: %s", p.id)
			p.Close()
		}

		if handler, ok := p.onICEConnectionStateChangeHandler.Load().(func()); ok && handler != nil {
			handler()
		}
	})

	return p, nil
}

func (p *Publisher) Answer(offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return webrtc.SessionDescription{}, err
	}

	for _, c := range p.candidates {
		p.pc.AddICECandidate(c)
	}
	p.candidates = nil

	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	return answer, nil
}

// GetRouter returns router with mediaSSRC
func (p *Publisher) GetRouter() Router {
	return p.router
}

// Close peer
func (p *Publisher) Close() {
	p.closeOnce.Do(func() {
		p.router.Stop()
		if err := p.pc.Close(); err != nil {
			log.Errorf("webrtc transport close err: %v", err)
		}
	})
}

// OnICECandidate handler
func (p *Publisher) OnICECandidate(f func(c *webrtc.ICECandidate)) {
	p.pc.OnICECandidate(f)
}

func (p *Publisher) OnICEConnectionStateChange(f func(connectionState webrtc.ICEConnectionState)) {
	p.onICEConnectionStateChangeHandler.Store(f)
}

func (p *Publisher) SignalingState() webrtc.SignalingState {
	return p.pc.SignalingState()
}

// AddICECandidate to peer connection
func (p *Publisher) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	if p.pc.RemoteDescription() != nil {
		return p.pc.AddICECandidate(candidate)
	}
	p.candidates = append(p.candidates, candidate)
	return nil
}
