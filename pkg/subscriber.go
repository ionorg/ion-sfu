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
		senders: make(map[string][]Sender),
	}

	dc, err := pc.CreateDataChannel(channelLabel, &webrtc.DataChannelInit{})
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

	if s.pendingSenders.Len() > 0 {
		pendingStart := make([]Sender, 0, s.pendingSenders.Len())

		s.Lock()
		for _, md := range pd.MediaDescriptions {
			if s.pendingSenders.Len() == 0 {
				break
			}
			mid, ok := md.Attribute(sdp.AttrKeyMID)
			if !ok {
				continue
			}
			for i := 0; i < s.pendingSenders.Len(); i++ {
				pd := s.pendingSenders.PopFront().(Sender)
				if pd.Transceiver().Mid() == mid {
					pendingStart = append(pendingStart, pd)
				} else {
					s.pendingSenders.PushBack(pd)
				}
			}
		}
		s.Unlock()

		if len(pendingStart) > 0 {
			defer func() {
				if err == nil {
					for _, ps := range pendingStart {
						ps.Start()
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

	for _, c := range s.candidates {
		s.pc.AddICECandidate(c)
	}
	s.candidates = nil

	return nil
}

func (s *Subscriber) GetSenders(streamID string) []Sender {
	s.RLock()
	defer s.RUnlock()
	return s.senders[streamID]
}

func (s *Subscriber) SetPendingSender(sender Sender) {
	s.Lock()
	s.pendingSenders.PushBack(sender)
	s.Unlock()
}

// Close peer
func (s *Subscriber) Close() error {
	return s.pc.Close()
}
