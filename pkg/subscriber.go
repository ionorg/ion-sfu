package sfu

import (
	"sync"
	"time"

	"github.com/bep/debounce"
	log "github.com/pion/ion-log"
	"github.com/pion/webrtc/v3"
)

type Subscriber struct {
	sync.RWMutex

	id string
	pc *webrtc.PeerConnection
	me *webrtc.MediaEngine

	tracks     map[string][]*DownTrack
	session    *Session
	channels   map[string]*webrtc.DataChannel
	candidates []webrtc.ICECandidateInit

	negotiate func()

	closeOnce sync.Once
}

// NewSubscriber creates a new Subscriber
func NewSubscriber(session *Session, id string, cfg WebRTCTransportConfig) (*Subscriber, error) {
	me, err := getSubscriberMediaEngine()
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

	s := &Subscriber{
		id:       id,
		me:       me,
		pc:       pc,
		session:  session,
		tracks:   make(map[string][]*DownTrack),
		channels: make(map[string]*webrtc.DataChannel),
	}

	dc, err := pc.CreateDataChannel(apiChannelLabel, &webrtc.DataChannelInit{})
	if err != nil {
		log.Errorf("DC creation error: %v", err)
		return nil, errPeerConnectionInitFailed
	}
	handleAPICommand(s, dc)

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Debugf("ice connection state: %s", connectionState)
		switch connectionState {
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

func (s *Subscriber) OnNegotiationNeeded(f func()) {
	debounced := debounce.New(250 * time.Millisecond)
	s.negotiate = func() {
		debounced(f)
	}
}

func (s *Subscriber) CreateOffer() (webrtc.SessionDescription, error) {
	offer, err := s.pc.CreateOffer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}

	err = s.pc.SetLocalDescription(offer)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}

	return offer, nil
}

// OnICECandidate handler
func (s *Subscriber) OnICECandidate(f func(c *webrtc.ICECandidate)) {
	s.pc.OnICECandidate(f)
}

// AddICECandidate to peer connection
func (s *Subscriber) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	if s.pc.RemoteDescription() != nil {
		return s.pc.AddICECandidate(candidate)
	}
	s.candidates = append(s.candidates, candidate)
	return nil
}

func (s *Subscriber) AddDownTrack(streamID string, downTrack *DownTrack) {
	s.Lock()
	defer s.Unlock()
	if senders, ok := s.tracks[streamID]; ok {
		senders = append(senders, downTrack)
		s.tracks[streamID] = senders
	} else {
		s.tracks[streamID] = []*DownTrack{downTrack}
	}
}

func (s *Subscriber) AddDataChannel(label string) (*webrtc.DataChannel, error) {
	s.Lock()
	defer s.Unlock()

	if s.channels[label] != nil {
		return s.channels[label], nil
	}

	dc, err := s.pc.CreateDataChannel(label, &webrtc.DataChannelInit{})
	if err != nil {
		log.Errorf("dc creation error: %v", err)
		return nil, errCreatingDataChannel
	}

	s.channels[label] = dc

	return dc, nil
}

// SetRemoteDescription sets the SessionDescription of the remote peer
func (s *Subscriber) SetRemoteDescription(desc webrtc.SessionDescription) error {
	if err := s.pc.SetRemoteDescription(desc); err != nil {
		log.Errorf("SetRemoteDescription error: %v", err)
		return err
	}

	for _, c := range s.candidates {
		s.pc.AddICECandidate(c)
	}
	s.candidates = nil

	return nil
}

func (s *Subscriber) GetDownTracks(streamID string) []*DownTrack {
	s.RLock()
	defer s.RUnlock()
	return s.tracks[streamID]
}

// Close peer
func (s *Subscriber) Close() error {
	return s.pc.Close()
}
