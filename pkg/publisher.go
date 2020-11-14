package sfu

import (
	"sync"

	log "github.com/pion/ion-log"
	"github.com/pion/webrtc/v3"
)

type Publisher struct {
	sync.Mutex

	id string
	pc *webrtc.PeerConnection

	router     Router
	session    *Session
	candidates []webrtc.ICECandidateInit

	onTrackHandler                    func(*webrtc.Track, *webrtc.RTPReceiver)
	onICEConnectionStateChangeHandler func(webrtc.ICEConnectionState)

	closeOnce sync.Once
	subOnce   sync.Once
}

// NewPublisher creates a new Publisher
func NewPublisher(session *Session, id string, me MediaEngine, cfg WebRTCTransportConfig) (*Publisher, error) {
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine), webrtc.WithSettingEngine(cfg.setting))
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

	pc.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		log.Debugf("Peer %s got remote track id: %s mediaSSRC: %d rid :%s streamID: %s", p.id, track.ID(), track.SSRC(), track.RID(), track.Label())
		if rr := p.router.AddReceiver(track, receiver); rr != nil {
			p.session.Publish(p.router, rr)
		}
		if p.onTrackHandler != nil {
			p.onTrackHandler(track, receiver)
		}
	})

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Debugf("ice connection state: %s", connectionState)
		switch connectionState {
		case webrtc.ICEConnectionStateConnected:
			p.subOnce.Do(func() {
				// Subscribe to existing peers
				p.session.Subscribe(p.id)
			})
		case webrtc.ICEConnectionStateFailed:
			fallthrough
		case webrtc.ICEConnectionStateClosed:
			p.closeOnce.Do(func() {
				log.Debugf("webrtc ice closed for peer: %s", p.id)
				if err := p.Close(); err != nil {
					log.Errorf("webrtc transport close err: %v", err)
				}
				p.router.Stop()
			})
		}

		if p.onICEConnectionStateChangeHandler != nil {
			p.onICEConnectionStateChangeHandler(connectionState)
		}
	})

	return p, nil
}

func (p *Publisher) Answer(offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	pd, err := offer.Unmarshal()
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	p.router.SetExtMap(pd)
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	return answer, nil
}

// ID of peer
func (p *Publisher) ID() string {
	return p.id
}

// GetRouter returns router with mediaSSRC
func (p *Publisher) GetRouter() Router {
	return p.router
}

// Close peer
func (p *Publisher) Close() error {
	return p.pc.Close()
}

// OnICECandidate handler
func (p *Publisher) OnICECandidate(f func(c *webrtc.ICECandidate)) {
	p.pc.OnICECandidate(f)
}

func (p *Publisher) OnICEConnectionStateChange(f func(connectionState webrtc.ICEConnectionState)) {
	p.onICEConnectionStateChangeHandler = f
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
