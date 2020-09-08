package sfu

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/lucsky/cuid"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

const (
	statCycle = 6 * time.Second
)

// WebRTCTransportConfig represents configuration options
type WebRTCTransportConfig struct {
	configuration webrtc.Configuration
	setting       webrtc.SettingEngine
}

// WebRTCTransport represents a sfu peer connection
type WebRTCTransport struct {
	id             string
	ctx            context.Context
	cancel         context.CancelFunc
	pc             *webrtc.PeerConnection
	me             MediaEngine
	mu             sync.RWMutex
	session        *Session
	routers        map[uint32]*Router
	onTrackHandler func(*webrtc.Track, *webrtc.RTPReceiver)
}

// NewWebRTCTransport creates a new WebRTCTransport
func NewWebRTCTransport(session *Session, me MediaEngine, cfg WebRTCTransportConfig) (*WebRTCTransport, error) {
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine), webrtc.WithSettingEngine(cfg.setting))
	pc, err := api.NewPeerConnection(cfg.configuration)

	if err != nil {
		log.Errorf("NewPeer error: %v", err)
		return nil, errPeerConnectionInitFailed
	}

	ctx, cancel := context.WithCancel(context.Background())
	p := &WebRTCTransport{
		id:      cuid.New(),
		ctx:     ctx,
		cancel:  cancel,
		pc:      pc,
		me:      me,
		session: session,
		routers: make(map[uint32]*Router),
	}

	// Subscribe to existing transports
	for _, t := range session.Transports() {
		for _, router := range t.Routers() {
			sender, err := p.NewSender(router.Track())
			log.Infof("Init add router ssrc %d to %s", router.Track().SSRC(), p.id)
			if err != nil {
				log.Errorf("Error subscribing to router %v", router)
				continue
			}
			router.AddSender(p.id, sender)
		}
	}

	// Add transport to the session
	session.AddTransport(p)

	pc.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver, streams []*webrtc.Stream) {
		log.Debugf("Peer %s got remote track id: %s ssrc: %d", p.id, track.ID(), track.SSRC())
		var recv Receiver
		switch track.Kind() {
		case webrtc.RTPCodecTypeVideo:
			recv = NewWebRTCVideoReceiver(ctx, config.Receiver.Video, track)
		case webrtc.RTPCodecTypeAudio:
			recv = NewWebRTCAudioReceiver(track)
		}

		if recv.Track().Kind() == webrtc.RTPCodecTypeVideo {
			go p.sendRTCP(recv)
		}

		router := NewRouter(p.id, recv)
		log.Debugf("Created router %s %d", p.id, recv.Track().SSRC())

		p.session.AddRouter(router)

		streams[0].OnRemoveTrack(func(track *webrtc.Track) {
			p.mu.Lock()
			defer p.mu.Unlock()
			r := p.routers[track.SSRC()]

			if r != nil {
				r.Close()
				delete(p.routers, track.SSRC())
			}
		})

		p.mu.Lock()
		p.routers[recv.Track().SSRC()] = router

		if p.onTrackHandler != nil {
			p.onTrackHandler(track, receiver)
		}
		p.mu.Unlock()
	})

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Debugf("ice connection state: %s", connectionState)
		switch connectionState {
		case webrtc.ICEConnectionStateDisconnected:
			log.Debugf("webrtc ice disconnected for peer: %s", p.id)
		case webrtc.ICEConnectionStateFailed:
			fallthrough
		case webrtc.ICEConnectionStateClosed:
			log.Debugf("webrtc ice closed for peer: %s", p.id)
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

// LocalDescription returns the peer connection LocalDescription
func (p *WebRTCTransport) LocalDescription() *webrtc.SessionDescription {
	return p.pc.LocalDescription()
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
	p.pc.OnNegotiationNeeded(f)
}

// OnTrack handler
func (p *WebRTCTransport) OnTrack(f func(*webrtc.Track, *webrtc.RTPReceiver)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onTrackHandler = f
}

// OnConnectionStateChange handler
func (p *WebRTCTransport) OnConnectionStateChange(f func(webrtc.PeerConnectionState)) {
	p.pc.OnConnectionStateChange(f)
}

// OnDataChannel handler
func (p *WebRTCTransport) OnDataChannel(f func(*webrtc.DataChannel)) {
	p.pc.OnDataChannel(f)
}

// AddTransceiverFromKind adds RtpTransceiver on WebRTC Transport
func (p *WebRTCTransport) AddTransceiverFromKind(kind webrtc.RTPCodecType, init ...webrtc.RtpTransceiverInit) (*webrtc.RTPTransceiver, error) {
	return p.pc.AddTransceiverFromKind(kind, init...)
}

// NewSender for peer
func (p *WebRTCTransport) NewSender(intrack *webrtc.Track) (Sender, error) {
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
	sender := NewWebRTCSender(p.ctx, outtrack, s)

	sender.OnClose(func() {
		err = p.pc.RemoveTrack(s)
		if err != nil {
			log.Errorf("Error closing sender: %s", err)
		}
	})

	return sender, nil
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

// GetRouter returns router with ssrc
func (p *WebRTCTransport) GetRouter(ssrc uint32) *Router {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.routers[ssrc]
}

// Close peer
func (p *WebRTCTransport) Close() error {
	p.mu.Lock()
	for rid, router := range p.routers {
		router.Close()
		delete(p.routers, rid)
	}
	p.mu.Unlock()

	p.session.RemoveTransport(p.id)
	p.cancel()
	return p.pc.Close()
}

func (p *WebRTCTransport) sendRTCP(recv Receiver) {
	for {
		pkt, err := recv.ReadRTCP()
		if err == io.ErrClosedPipe {
			return
		}

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
