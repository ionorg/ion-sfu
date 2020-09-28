package sfu

import (
	"context"
	"fmt"
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
	me             webrtc.MediaEngine
	mu             sync.RWMutex
	session        *Session
	senders        []Sender
	routers        map[string]Router
	onTrackHandler func(*webrtc.Track, *webrtc.RTPReceiver)
	// Custom label for simulcast
	label string
}

// NewWebRTCTransport creates a new WebRTCTransport
func NewWebRTCTransport(ctx context.Context, session *Session, me webrtc.MediaEngine, cfg WebRTCTransportConfig) (*WebRTCTransport, error) {
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(cfg.setting))
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
		label:   cuid.New(),
	}

	// Subscribe to existing transports
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

	// Add transport to the session
	session.AddTransport(p)

	pc.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		log.Debugf("Peer %s got remote track id: %s ssrc: %d rid :%s label: %s", p.id, track.ID(), track.SSRC(), track.RID(), track.Label())
		recv := NewWebRTCReceiver(ctx, track, cfg.router)

		if recv.Track().Kind() == webrtc.RTPCodecTypeVideo {
			go p.sendRTCP(recv)
		}
		if router, ok := p.routers[track.ID()]; !ok {
			if track.RID() != "" {
				router = newRouter(p.id, cfg.router, SimulcastRouter)
				go func() {
					// Send 3 big remb msgs to fwd all the tracks
					ticker := time.NewTicker(1 * time.Second)
					var ctr uint8
					for range ticker.C {
						ctr++
						if writeErr := pc.WriteRTCP([]rtcp.Packet{&rtcp.ReceiverEstimatedMaximumBitrate{Bitrate: 10000000, SenderSSRC: track.SSRC()}}); writeErr != nil {
							log.Errorf("Sending simulcast remb error: %v", err)
						}
						if ctr == 3 {
							ticker.Stop()
						}
					}
				}()
			} else {
				router = newRouter(p.id, cfg.router, SimpleRouter)
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
		fmt.Printf("New DataChannel %s %d\n", d.Label(), d.ID())
		// Register text message handling
		d.OnMessage(func(msg webrtc.DataChannelMessage) {
		})
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

func (p *WebRTCTransport) stats() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	info := fmt.Sprintf("  peer: %s\n", p.id)
	// for _, router := range p.routers {
	info += "" // router.stats()
	// }

	return info
}
