package sfu

import (
	"context"
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
	router        RouterConfig
}

// WebRTCTransport represents a sfu peer connection
type WebRTCTransport struct {
	id             string
	ctx            context.Context
	cancel         context.CancelFunc
	pc             *webrtc.PeerConnection
	me             MediaEngine
	mu             sync.RWMutex
	candidates     []webrtc.ICECandidateInit
	session        *Session
	senders        map[string][]Sender
	routers        map[string]Router
	onTrackHandler func(*webrtc.Track, *webrtc.RTPReceiver)
}

// NewWebRTCTransport creates a new WebRTCTransport
func NewWebRTCTransport(ctx context.Context, session *Session, me MediaEngine, cfg WebRTCTransportConfig) (*WebRTCTransport, error) {
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine), webrtc.WithSettingEngine(cfg.setting))
	pc, err := api.NewPeerConnection(cfg.configuration)

	if err != nil {
		log.Errorf("NewPeer error: %v", err)
		return nil, errPeerConnectionInitFailed
	}

	ctx, cancel := context.WithCancel(ctx)
	p := &WebRTCTransport{
		id:      cuid.New(),
		ctx:     ctx,
		cancel:  cancel,
		pc:      pc,
		me:      me,
		session: session,
		routers: make(map[string]Router),
		senders: make(map[string][]Sender),
	}

	// Subscribe to existing transports
	defer func() {
		for _, t := range session.Transports() {
			for _, router := range t.Routers() {
				err := router.AddSender(p)
				// log.Infof("Init add router ssrc %d to %s", router.receivers[0].Track().SSRC(), p.id)
				if err != nil {
					log.Errorf("Error subscribing to router err: %v", err)
					continue
				}
			}
		}
	}()

	// Add transport to the session
	session.AddTransport(p)

	pc.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		log.Debugf("Peer %s got remote track id: %s ssrc: %d rid :%s streamID: %s", p.id, track.ID(), track.SSRC(), track.RID(), track.Label())
		recv := NewWebRTCReceiver(ctx, track, cfg.router)

		if recv.Track().Kind() == webrtc.RTPCodecTypeVideo {
			go p.sendRTCP(recv)
		}
		if router, ok := p.routers[track.ID()]; !ok {
			if track.RID() != "" {
				router = newRouter(p.id, track.Label(), cfg.router, SimulcastRouter)
				go func() {
					// Send 3 big remb msgs to fwd all the tracks
					ticker := time.NewTicker(3 * time.Second)
					for range ticker.C {
						if writeErr := pc.WriteRTCP([]rtcp.Packet{&rtcp.ReceiverEstimatedMaximumBitrate{Bitrate: 1500000, SenderSSRC: track.SSRC()}}); writeErr != nil {
							return
						}
					}
				}()
			} else {
				router = newRouter(p.id, track.Label(), cfg.router, SimpleRouter)
			}
			router.AddReceiver(recv)
			p.session.AddRouter(router)
			p.mu.Lock()
			p.routers[recv.Track().ID()] = router
			p.mu.Unlock()
			log.Debugf("Created router %s %d", p.id, recv.Track().SSRC())
		} else {
			router.AddReceiver(recv)
		}

		recv.OnCloseHandler(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			delete(p.routers, track.ID())
		})

		if p.onTrackHandler != nil {
			p.onTrackHandler(track, receiver)
		}
	})

	// Register data channel creation handling
	pc.OnDataChannel(func(d *webrtc.DataChannel) {
		log.Debugf("New DataChannel %s %d\n", d.Label(), d.ID())
		// Register text message handling
		if d.Label() == channelLabel {
			handleAPICommand(p, d)
		}
	})

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Debugf("ice connection state: %s", connectionState)
		select {
		case <-p.ctx.Done():
			return
		default:
			switch connectionState {
			case webrtc.ICEConnectionStateDisconnected:
				log.Debugf("webrtc ice disconnected for peer: %s", p.id)
			case webrtc.ICEConnectionStateFailed:
				fallthrough
			case webrtc.ICEConnectionStateClosed:
				log.Debugf("webrtc ice closed for peer: %s", p.id)
				if err := p.Close(); err != nil {
					log.Errorf("webrtc transport close err: %v", err)
				}
			}
		}
	})

	return p, nil
}

// CreateOffer generates the localDescription
func (p *WebRTCTransport) CreateOffer() (webrtc.SessionDescription, error) {
	return p.pc.CreateOffer(nil)
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

	if len(p.candidates) > 0 {
		for _, candidate := range p.candidates {
			err := p.pc.AddICECandidate(candidate)
			if err != nil {
				log.Errorf("Error adding ice candidate %s", err)
			}
		}
		p.candidates = nil
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

// ID of peer
func (p *WebRTCTransport) ID() string {
	return p.id
}

// Routers returns routers for this peer
func (p *WebRTCTransport) Routers() map[string]Router {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.routers
}

// GetRouter returns router with ssrc
func (p *WebRTCTransport) GetRouter(trackID string) Router {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.routers[trackID]
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
	p.cancel()
	return p.pc.Close()
}

func (p *WebRTCTransport) sendRTCP(recv Receiver) {
	for pkt := range recv.ReadRTCP() {
		log.Tracef("sendRTCP %v", pkt)
		if err := p.pc.WriteRTCP([]rtcp.Packet{pkt}); err != nil {
			log.Errorf("Error writing RTCP %s", err)
		}
	}
}
