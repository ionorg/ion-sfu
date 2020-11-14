package sfu

import (
	"net/url"
	"sync"
	"time"

	"github.com/pion/sdp/v3"

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

type pendingSender struct {
	transceiver *webrtc.RTPTransceiver
	sender      Sender
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
				// Subscribe to existing peers
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

	pc.GetMapExtension(func() map[webrtc.SDPSectionType][]sdp.ExtMap {
		sdesMid, _ := url.Parse(sdp.SDESMidURI)
		ext := make(map[webrtc.SDPSectionType][]sdp.ExtMap)

		for _, t := range pc.GetTransceivers() {
			switch t.Kind() {
			case webrtc.RTPCodecTypeAudio:
				if t.Direction() == webrtc.RTPTransceiverDirectionSendonly {
					ext[webrtc.SDPSectionType(t.Mid())] = []sdp.ExtMap{
						{
							Value: 1,
							URI:   sdesMid,
						},
					}
				}
			case webrtc.RTPCodecTypeVideo:
				if t.Direction() == webrtc.RTPTransceiverDirectionSendonly {
					ext[webrtc.SDPSectionType(t.Mid())] = []sdp.ExtMap{
						{
							Value: 1,
							URI:   sdesMid,
						},
					}
				}
			}
		}

		return ext
	})

	return s, nil
}

func (s *Subscriber) OnNegotiationNeeded(f func()) {
	debounced := debounce.New(100 * time.Millisecond)
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

func (s *Subscriber) AddSender(streamID string, sender Sender) {
	s.Lock()
	defer s.Unlock()
	if senders, ok := s.senders[streamID]; ok {
		senders = append(senders, sender)
		s.senders[streamID] = senders
	} else {
		s.senders[streamID] = []Sender{sender}
	}
}

// SetRemoteDescription sets the SessionDescription of the remote peer
func (s *Subscriber) SetRemoteDescription(desc webrtc.SessionDescription) error {
	var err error
	pd, err := desc.Unmarshal()
	if err != nil {
		log.Errorf("SetRemoteDescription error: %v", err)
		return err
	}

	s.Lock()
	defer s.Unlock()
	if s.pendingSenders.Len() > 0 {
		pendingStart := make([]pendingSender, 0, s.pendingSenders.Len())
		for _, md := range pd.MediaDescriptions {
			if s.pendingSenders.Len() == 0 {
				break
			}
			mid, ok := md.Attribute(sdp.AttrKeyMID)
			if !ok {
				continue
			}
			for i := 0; i < s.pendingSenders.Len(); i++ {
				pd := s.pendingSenders.PopFront().(pendingSender)
				if pd.transceiver.Mid() == mid {
					pendingStart = append(pendingStart, pd)
				} else {
					s.pendingSenders.PushBack(pd)
				}
			}
		}
		if len(pendingStart) > 0 {
			defer func() {
				if err == nil {
					for _, ps := range pendingStart {
						ps.sender.Start()
					}
				} else {
					s.Lock()
					for _, ps := range pendingStart {
						s.pendingSenders.PushBack(ps)
					}
					s.Unlock()
				}
			}()
		}
	}

	err = s.pc.SetRemoteDescription(desc)
	if err != nil {
		log.Errorf("SetRemoteDescription error: %v", err)
		return err
	}

	return nil
}

func (s *Subscriber) GetSenders(streamID string) []Sender {
	s.RLock()
	defer s.RUnlock()
	return s.senders[streamID]
}

// Close peer
func (s *Subscriber) Close() error {
	return s.pc.Close()
}
