package sfu

import (
	"fmt"
	"sync"
	"time"

	"github.com/lucsky/cuid"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/util"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

const (
	statCycle = 6 * time.Second
)

// WebRTCTransport represents a sfu peer connection
type WebRTCTransport struct {
	id                         string
	pc                         *webrtc.PeerConnection
	me                         MediaEngine
	mu                         sync.RWMutex
	stop                       bool
	routers                    map[uint32]*Router
	onCloseHandler             func()
	onNegotiationNeededHandler func()
	onRouterHander             func(*Router)
}

// NewWebRTCTransport creates a new WebRTCTransport
func NewWebRTCTransport(offer webrtc.SessionDescription) (*WebRTCTransport, error) {
	// We make our own mediaEngine so we can place the sender's codecs in it.  This because we must use the
	// dynamic media type from the sender in our answer. This is not required if we are the offerer
	me := MediaEngine{}
	if err := me.PopulateFromSDP(offer); err != nil {
		return nil, errSdpParseFailed
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine), webrtc.WithSettingEngine(setting))
	pc, err := api.NewPeerConnection(cfg)

	if err != nil {
		log.Errorf("NewPeer error: %v", err)
		return nil, errPeerConnectionInitFailed
	}

	p := &WebRTCTransport{
		id:      cuid.New(),
		pc:      pc,
		me:      me,
		routers: make(map[uint32]*Router),
	}

	pc.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		log.Debugf("Peer %s got remote track %v", p.id, track)
		var recv Receiver
		switch track.Kind() {
		case webrtc.RTPCodecTypeVideo:
			recv = NewWebRTCVideoReceiver(config.Receiver.Video, track)
		case webrtc.RTPCodecTypeAudio:
			recv = NewWebRTCAudioReceiver(track)
		}

		if recv.Track().Kind() == webrtc.RTPCodecTypeVideo {
			go p.sendRTCP(recv)
		}

		router := NewRouter(recv)

		p.mu.Lock()
		p.routers[recv.Track().SSRC()] = router
		p.mu.Unlock()

		log.Debugf("Create router %s %d", p.id, recv.Track().SSRC())

		if p.onRouterHander != nil {
			p.onRouterHander(router)
		}
	})

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Infof("ice connection state: %s", connectionState)
		switch connectionState {
		case webrtc.ICEConnectionStateDisconnected:
			log.Infof("webrtc ice disconnected for peer: %s", p.id)
		case webrtc.ICEConnectionStateFailed:
			fallthrough
		case webrtc.ICEConnectionStateClosed:
			log.Infof("webrtc ice closed for peer: %s", p.id)
			p.Close()
		}
	})

	return p, nil
}

// CreateOffer generates the localDescription
func (p *WebRTCTransport) CreateOffer() (webrtc.SessionDescription, error) {
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		log.Errorf("CreateOffer error: %v", err)
		return webrtc.SessionDescription{}, err
	}

	return offer, nil
}

// SetLocalDescription sets the SessionDescription of the remote peer
func (p *WebRTCTransport) SetLocalDescription(desc webrtc.SessionDescription) error {
	err := p.pc.SetLocalDescription(desc)
	if err != nil {
		log.Errorf("SetLocalDescription error: %v", err)
		return err
	}

	return nil
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
	err := p.pc.SetRemoteDescription(desc)
	if err != nil {
		log.Errorf("SetRemoteDescription error: %v", err)
		return err
	}

	return nil
}

// OnClose is called when the peer is closed
func (p *WebRTCTransport) OnClose(f func()) {
	p.onCloseHandler = f
}

// OnRouter handler called when a router is added
func (p *WebRTCTransport) OnRouter(f func(*Router)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onRouterHander = f
}

// AddICECandidate to peer connection
func (p *WebRTCTransport) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	return p.pc.AddICECandidate(candidate)
}

// OnICECandidate handler
func (p *WebRTCTransport) OnICECandidate(f func(c *webrtc.ICECandidate)) {
	p.pc.OnICECandidate(f)
}

// OnNegotiationNeeded handler
func (p *WebRTCTransport) OnNegotiationNeeded(f func()) {
	var debounced = util.NewDebouncer(500 * time.Millisecond)
	p.onNegotiationNeededHandler = func() {
		debounced(f)
	}
}

// NewSender for peer
func (p *WebRTCTransport) NewSender(intrack Track) (Sender, error) {
	to := p.me.GetCodecsByName(intrack.Codec().Name)

	if len(to) == 0 {
		log.Errorf("Error mapping payload type")
		return nil, errPtNotSupported
	}

	pt := to[0].PayloadType

	log.Debugf("Creating track: %d %d %s %s", pt, intrack.SSRC(), intrack.ID(), intrack.Label())
	outtrack, err := p.pc.NewTrack(pt, intrack.SSRC(), intrack.ID(), intrack.Label())

	if err != nil {
		log.Errorf("Error creating track")
		return nil, err
	}

	s, err := p.pc.AddTrack(outtrack)

	if err != nil {
		log.Errorf("Error adding send track")
		return nil, err
	}

	// Create webrtc sender for the peer we are sending track to
	sender := NewWebRTCSender(outtrack, s)

	return sender, nil
}

// AddSub adds peer as a sub
func (p *WebRTCTransport) AddSub(t Transport) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, router := range p.routers {
		sender, err := t.NewSender(router.Track())
		if err != nil {
			log.Errorf("Error subscribing transport %s to router %v", t.ID(), router)
		}
		router.AddSender(t.ID(), sender)
	}
}

// ID of peer
func (p *WebRTCTransport) ID() string {
	return p.id
}

// Routers returns routers for this peer
func (p *WebRTCTransport) Routers() map[uint32]*Router {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.routers
}

// Close peer
func (p *WebRTCTransport) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stop {
		return nil
	}

	for _, router := range p.routers {
		router.Close()
	}

	if p.onCloseHandler != nil {
		p.onCloseHandler()
	}
	p.stop = true
	return p.pc.Close()
}

func (p *WebRTCTransport) sendRTCP(recv Receiver) {
	for {
		p.mu.RLock()
		if p.stop {
			p.mu.RUnlock()
			return
		}
		p.mu.RUnlock()

		pkt, err := recv.ReadRTCP()
		if err != nil {
			log.Errorf("Error reading RTCP %s", err)
			continue
		}

		log.Tracef("sendRTCP %v", pkt)
		err = p.pc.WriteRTCP([]rtcp.Packet{pkt})
		if err != nil {
			log.Errorf("Error writing RTCP %s", err)
		}
	}
}

func (p *WebRTCTransport) stats() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	info := fmt.Sprintf("  peer: %s\n", p.id)
	for _, router := range p.routers {
		info += router.stats()
	}

	return info
}
