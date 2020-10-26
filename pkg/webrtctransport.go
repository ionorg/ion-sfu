package sfu

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bep/debounce"
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
	ctx            context.Context
	cancel         context.CancelFunc
	pc             *webrtc.PeerConnection
	me             MediaEngine
	mu             sync.RWMutex
	candidates     []webrtc.ICECandidateInit
	session        *Session
	mids           map[string]Sender
	senders        map[string][]Sender
	router         Router
	onTrackHandler func(*webrtc.Track, *webrtc.RTPReceiver)

	subOnce sync.Once
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
	id := cuid.New()
	p := &WebRTCTransport{
		id:      id,
		ctx:     ctx,
		cancel:  cancel,
		pc:      pc,
		me:      me,
		session: session,
		router:  newRouter(pc, id, cfg.router),
		mids:    make(map[string]Sender),
		senders: make(map[string][]Sender),
	}

	// Add transport to the session
	session.AddTransport(p)

	pc.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		log.Debugf("Peer %s got remote track id: %s mediaSSRC: %d rid :%s streamID: %s", p.id, track.ID(), track.SSRC(), track.RID(), track.Label())
		if rr := p.router.AddReceiver(ctx, track, receiver); rr != nil {
			p.session.AddRouter(p.router, rr)
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
		select {
		case <-p.ctx.Done():
			return
		default:
			switch connectionState {
			case webrtc.ICEConnectionStateConnected:
				p.subOnce.Do(func() {
					// Subscribe to existing transports
					for _, t := range session.Transports() {
						if t.ID() == p.id {
							continue
						}
						err := t.GetRouter().AddSender(p, nil)
						if err != nil {
							log.Errorf("Subscribing to router err: %v", err)
							continue
						}
					}
				})
			case webrtc.ICEConnectionStateDisconnected:
				log.Debugf("webrtc ice disconnected for peer: %s", p.id)
			case webrtc.ICEConnectionStateFailed:
				fallthrough
			case webrtc.ICEConnectionStateClosed:
				log.Debugf("webrtc ice closed for peer: %s", p.id)
				if err := p.Close(); err != nil {
					log.Errorf("webrtc transport close err: %v", err)
				}
				p.router.Stop()
			}
		}
	})

	return p, nil
}

// CreateOffer generates the localDescription
func (p *WebRTCTransport) CreateOffer() (webrtc.SessionDescription, error) {
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	parsed := sdp.SessionDescription{}
	if err := parsed.Unmarshal([]byte(offer.SDP)); err == nil {
		for _, md := range parsed.MediaDescriptions {
			if md.MediaName.Media != mediaNameAudio && md.MediaName.Media != mediaNameVideo {
				continue
			}
			var msid, mid string

			for _, att := range md.Attributes {
				switch att.Key {
				case sdp.AttrKeyMID:
					mid = att.Value
					if len(msid) > 0 {
						break
					}
				case sdp.AttrKeyMsid:
					msid = att.Value
					if len(mid) > 0 {
						break
					}
				}
			}
			if len(msid) > 0 && len(mid) > 0 {
				split := strings.Split(msid, " ")
				sid := split[0]
				tid := split[1]
				// find sender for mid
				for _, sender := range p.senders[sid] {
					if sender.Track().ID() == tid {
						p.mids[mid] = sender
					}
				}
			}
		}
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
	pd, err := desc.Unmarshal()
	if err != nil {
		log.Errorf("SetRemoteDescription error: %v", err)
		return err
	}
	err = p.pc.SetRemoteDescription(desc)
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

	for _, md := range pd.MediaDescriptions {
		if md.MediaName.Media != mediaNameAudio && md.MediaName.Media != mediaNameVideo {
			continue
		}
		var (
			ext int
			id  string
		)

		for _, att := range md.Attributes {
			if att.Key == sdp.AttrKeyMID {
				if p.mids[att.Value] != nil {
					p.mids[att.Value].Start()
					// remove mid mapping in case transceiver is reused later
					p.mids[att.Value] = nil
				}
			}

			if att.Key == sdp.AttrKeyExtMap && strings.HasSuffix(att.Value, sdp.TransportCCURI) {
				ext, _ = strconv.Atoi(att.Value[:1])
				if len(id) > 0 {
					break
				}
			}
			if att.Key == sdp.AttrKeyMsid {
				v := strings.Split(att.Value, " ")
				id = v[len(v)-1]
				if ext != 0 {
					break
				}
			}
		}
		p.router.AddTWCCExt(id, ext)

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
	p.pc.OnNegotiationNeeded(func() {
		debounced(f)
	})
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
	p.cancel()
	return p.pc.Close()
}
