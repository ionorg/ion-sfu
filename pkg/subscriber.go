package sfu

import (
	"sync"
	"time"

	"github.com/bep/debounce"

	log "github.com/pion/ion-log"

	"github.com/gammazero/deque"
	"github.com/pion/webrtc/v3"
)

type Subscriber struct {
	sync.RWMutex

	id string
	pc *webrtc.PeerConnection
	me MediaEngine

	session    *Session
	senders    map[string][]Sender
	candidates []webrtc.ICECandidateInit

	onTrackHandler func(*webrtc.Track, *webrtc.RTPReceiver)
	pendingSenders deque.Deque
	negotiate      func()

	subOnce   sync.Once
	closeOnce sync.Once
}

// NewSubscriber creates a new Subscriber
func NewSubscriber(session *Session, id string, me MediaEngine, cfg WebRTCTransportConfig) (*Subscriber, error) {
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine), webrtc.WithSettingEngine(cfg.setting))
	pc, err := api.NewPeerConnection(cfg.configuration)

	if err != nil {
		log.Errorf("NewPeer error: %v", err)
		return nil, errPeerConnectionInitFailed
	}

	s := &Subscriber{
		id:      id,
		me:      me,
		pc:      pc,
		session: session,
	}

	pc.OnDataChannel(func(d *webrtc.DataChannel) {
		log.Debugf("New DataChannel %s %d", d.Label(), d.ID())
		// Register text message handling
		if d.Label() == channelLabel {
			handleAPICommand(s, d)
		}
	})

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Debugf("ice connection state: %s", connectionState)
		switch connectionState {
		case webrtc.ICEConnectionStateConnected:
			s.subOnce.Do(func() {
				// Subscribe to existing publishers
				s.session.Subscribe(s)
			})
		case webrtc.ICEConnectionStateFailed:
			fallthrough
		case webrtc.ICEConnectionStateClosed:
			s.closeOnce.Do(func() {
				log.Debugf("webrtc ice closed for peer: %s", s.id)
				if err := s.Close(); err != nil {
					log.Errorf("webrtc transport close err: %v", err)
				}
			})
		}
	})

	return s, nil
}

func (p *Subscriber) OnNegotiationNeeded(f func()) {
	debounced := debounce.New(100 * time.Millisecond)
	p.negotiate = func() {
		debounced(f)
	}
}

func (p *Subscriber) CreateOffer() (webrtc.SessionDescription, error) {
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}

	err = p.pc.SetLocalDescription(offer)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}

	return offer, nil
}

// OnICECandidate handler
func (p *Subscriber) OnICECandidate(f func(c *webrtc.ICECandidate)) {
	p.pc.OnICECandidate(f)
}

// AddICECandidate to peer connection
func (p *Subscriber) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	if p.pc.RemoteDescription() != nil {
		return p.pc.AddICECandidate(candidate)
	}
	p.candidates = append(p.candidates, candidate)
	return nil
}

func (p *Subscriber) AddSender(streamID string, sender Sender) {
	p.Lock()
	defer p.Unlock()
	if senders, ok := p.senders[streamID]; ok {
		senders = append(senders, sender)
		p.senders[streamID] = senders
	} else {
		p.senders[streamID] = []Sender{sender}
	}
}

func (p *Subscriber) GetSenders(streamID string) []Sender {
	p.RLock()
	defer p.RUnlock()
	return p.senders[streamID]
}

// Close peer
func (p *Subscriber) Close() error {
	return p.pc.Close()
}
