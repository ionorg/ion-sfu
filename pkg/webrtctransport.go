package sfu

import (
	"sync"
	"time"

	"github.com/bep/debounce"
	"github.com/gammazero/deque"

	"github.com/lucsky/cuid"
	log "github.com/pion/ion-log"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
)

// WebRTCTransportConfig represents configuration options
type WebRTCTransportConfig struct {
	configuration webrtc.Configuration
	setting       webrtc.SettingEngine
	router        RouterConfig
}

// WebRTCTransport represents a sfu peer connection
type WebRTCTransport struct {
	id             string
	pc             *webrtc.PeerConnection
	me             MediaEngine
	mu             sync.RWMutex
	router         Router
	session        *Session
	senders        map[string][]Sender
	candidates     []webrtc.ICECandidateInit
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

// NewWebRTCTransport creates a new WebRTCTransport
func NewWebRTCTransport(session *Session, me MediaEngine, cfg WebRTCTransportConfig) (*WebRTCTransport, error) {
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine), webrtc.WithSettingEngine(cfg.setting))
	pc, err := api.NewPeerConnection(cfg.configuration)

	if err != nil {
		log.Errorf("NewPeer error: %v", err)
		return nil, errPeerConnectionInitFailed
	}

	id := cuid.New()
	p := &WebRTCTransport{
		id:      id,
		pc:      pc,
		me:      me,
		session: session,
		router:  newRouter(pc, id, cfg.router),
		senders: make(map[string][]Sender),
	}
	p.pendingSenders.SetMinCapacity(2)

	// Add transport to the session
	session.AddTransport(p)

	pc.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		log.Debugf("Peer %s got remote track id: %s mediaSSRC: %d rid :%s streamID: %s", p.id, track.ID(), track.SSRC(), track.RID(), track.Label())
		if rr := p.router.AddReceiver(track, receiver); rr != nil {
			p.session.Publish(p.router, rr)
		}
		if p.onTrackHandler != nil {
			p.onTrackHandler(track, receiver)
		}
	})

	pc.OnDataChannel(func(d *webrtc.DataChannel) {
		log.Debugf("New DataChannel %s %d", d.Label(), d.ID())
		// Register text message handling
		if d.Label() == channelLabel {
			handleAPICommand(p, d)
		}
	})

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Debugf("ice connection state: %s", connectionState)
		switch connectionState {
		case webrtc.ICEConnectionStateConnected:
			p.subOnce.Do(func() {
				// Subscribe to existing transports
				p.session.Subscribe(p)
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
	})

	pc.GetMapExtension(p.router.OfferExtMap)

	return p, nil
}

// CreateOffer generates the localDescription
func (p *WebRTCTransport) CreateOffer() (webrtc.SessionDescription, error) {
	return p.pc.CreateOffer(nil)
}

// SetLocalDescription sets the SessionDescription of the remote peer
func (p *WebRTCTransport) SetLocalDescription(desc webrtc.SessionDescription) error {
	return p.pc.SetLocalDescription(desc)
}

// CreateAnswer generates the localDescription
func (p *WebRTCTransport) CreateAnswer() (webrtc.SessionDescription, error) {
	offer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		log.Errorf("CreateAnswer error: %v", err)
		return webrtc.SessionDescription{}, err
	}

	return offer, nil
}

// SetRemoteDescription sets the SessionDescription of the remote peer
func (p *WebRTCTransport) SetRemoteDescription(desc webrtc.SessionDescription) error {
	var err error
	pd, err := desc.Unmarshal()
	if err != nil {
		log.Errorf("SetRemoteDescription error: %v", err)
		return err
	}

	switch desc.Type {
	case webrtc.SDPTypeAnswer:
		p.router.SetExtMap(pd)
		p.mu.Lock()
		if p.pendingSenders.Len() > 0 {
			pendingStart := make([]pendingSender, 0, p.pendingSenders.Len())
			for _, md := range pd.MediaDescriptions {
				if p.pendingSenders.Len() == 0 {
					break
				}
				mid, ok := md.Attribute(sdp.AttrKeyMID)
				if !ok {
					continue
				}
				for i := 0; i < p.pendingSenders.Len(); i++ {
					pd := p.pendingSenders.PopFront().(pendingSender)
					if pd.transceiver.Mid() == mid {
						pendingStart = append(pendingStart, pd)
					} else {
						p.pendingSenders.PushBack(pd)
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
						p.mu.Lock()
						for _, ps := range pendingStart {
							p.pendingSenders.PushBack(ps)
						}
						p.mu.Unlock()
					}
				}()
			}
		}
		p.mu.Unlock()
	case webrtc.SDPTypeOffer:
		p.router.SetExtMap(pd)
	}

	err = p.pc.SetRemoteDescription(desc)
	if err != nil {
		log.Errorf("SetRemoteDescription error: %v", err)
		return err
	}

	return nil
}

// LocalDescription returns the peer connection LocalDescription
func (p *WebRTCTransport) LocalDescription() *webrtc.SessionDescription {
	return p.pc.LocalDescription()
}

// AddICECandidate to peer connection
func (p *WebRTCTransport) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	if p.pc.RemoteDescription() != nil {
		return p.pc.AddICECandidate(candidate)
	}
	p.candidates = append(p.candidates, candidate)
	return nil
}

// OnICECandidate handler
func (p *WebRTCTransport) OnICECandidate(f func(c *webrtc.ICECandidate)) {
	p.pc.OnICECandidate(f)
}

// OnNegotiationNeeded handler
func (p *WebRTCTransport) OnNegotiationNeeded(f func()) {
	debounced := debounce.New(100 * time.Millisecond)
	p.negotiate = func() {
		debounced(f)
	}
}

// OnTrack handler
func (p *WebRTCTransport) OnTrack(f func(*webrtc.Track, *webrtc.RTPReceiver)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onTrackHandler = f
}

func (p *WebRTCTransport) SignalingState() webrtc.SignalingState {
	return p.pc.SignalingState()
}

// ID of peer
func (p *WebRTCTransport) ID() string {
	return p.id
}

// GetRouter returns router with mediaSSRC
func (p *WebRTCTransport) GetRouter() Router {
	return p.router
}

func (p *WebRTCTransport) AddSender(streamID string, sender Sender) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if senders, ok := p.senders[streamID]; ok {
		senders = append(senders, sender)
		p.senders[streamID] = senders
	} else {
		p.senders[streamID] = []Sender{sender}
	}
}

func (p *WebRTCTransport) GetSenders(streamID string) []Sender {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.senders[streamID]
}

// Close peer
func (p *WebRTCTransport) Close() error {
	p.session.RemoveTransport(p.id)
	return p.pc.Close()
}
