package sfu

import (
	"fmt"
	"sync"
	"time"

	"github.com/lucsky/cuid"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v2"
)

const (
	statCycle = 6 * time.Second
)

// Peer represents a sfu peer connection
type Peer struct {
	id             string
	pc             *webrtc.PeerConnection
	me             MediaEngine
	routers        map[uint32]*Router
	routersLock    sync.RWMutex
	onCloseHandler func()
	onRouterHander func(*Router)
}

// NewPeer creates a new Peer
func NewPeer(offer webrtc.SessionDescription) (*Peer, error) {
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

	p := &Peer{
		id:      cuid.New(),
		pc:      pc,
		me:      me,
		routers: make(map[uint32]*Router),
	}

	pc.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		log.Infof("Peer %s got remote track %v", p.id, track)
		var recv Receiver
		switch track.Kind() {
		case webrtc.RTPCodecTypeVideo:
			recv = NewVideoReceiver(track)
		case webrtc.RTPCodecTypeAudio:
			recv = NewAudioReceiver(track)
		}

		if recv.Track().Kind() == webrtc.RTPCodecTypeVideo {
			go p.sendRTCP(recv)
		}

		router := NewRouter(recv)

		p.routersLock.Lock()
		p.routers[recv.Track().SSRC()] = router
		p.routersLock.Unlock()

		log.Infof("Create router %s %d", p.id, recv.Track().SSRC())

		if p.onRouterHander != nil {
			p.onRouterHander(router)
		}
	})

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
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
func (p *Peer) CreateOffer() (webrtc.SessionDescription, error) {
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		log.Errorf("CreateOffer error: %v", err)
		return webrtc.SessionDescription{}, err
	}

	return offer, nil
}

// SetLocalDescription sets the SessionDescription of the remote peer
func (p *Peer) SetLocalDescription(desc webrtc.SessionDescription) error {
	err := p.pc.SetLocalDescription(desc)
	if err != nil {
		log.Errorf("SetLocalDescription error: %v", err)
		return err
	}

	return nil
}

// CreateAnswer generates the localDescription
func (p *Peer) CreateAnswer() (webrtc.SessionDescription, error) {
	offer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		log.Errorf("CreateAnswer error: %v", err)
		return webrtc.SessionDescription{}, err
	}

	return offer, nil
}

// SetRemoteDescription sets the SessionDescription of the remote peer
func (p *Peer) SetRemoteDescription(desc webrtc.SessionDescription) error {
	err := p.pc.SetRemoteDescription(desc)
	if err != nil {
		log.Errorf("SetRemoteDescription error: %v", err)
		return err
	}

	return nil
}

// Offer ..
func (p *Peer) Offer() (webrtc.SessionDescription, error) {
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		log.Errorf("Offer error: p.pc.CreateOffer %v", err)
		return webrtc.SessionDescription{}, err
	}

	err = p.pc.SetLocalDescription(offer)
	if err != nil {
		log.Errorf("Offer error: p.pc.SetLocalDescription offer=%v err=%v", offer, err)
		return webrtc.SessionDescription{}, err
	}

	return offer, nil
}

// OnClose is called when the peer is closed
func (p *Peer) OnClose(f func()) {
	p.onCloseHandler = f
}

// OnRouter handler called when a router is added
func (p *Peer) OnRouter(f func(*Router)) {
	log.Infof("add on track")
	p.onRouterHander = f
}

// AddICECandidate to peer connection
func (p *Peer) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	return p.pc.AddICECandidate(candidate)
}

// OnICECandidate handler
func (p *Peer) OnICECandidate(handler func(c *webrtc.ICECandidate)) {
	p.pc.OnICECandidate(handler)
}

// NewSender on this peer
func (p *Peer) NewSender(track *webrtc.Track) (*Sender, error) {
	pt, ok := p.me.GetPayloadType(track.Codec().Name)

	if !ok {
		log.Errorf("Error mapping payload type")
		return nil, errPtNotSupported
	}

	track, err := p.pc.NewTrack(pt, track.SSRC(), track.ID(), track.Label())

	if err != nil {
		log.Errorf("Error creating track")
		return nil, err
	}

	trans, err := p.pc.AddTransceiverFromTrack(track, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
		SendEncodings: []webrtc.RTPEncodingParameters{{
			RTPCodingParameters: webrtc.RTPCodingParameters{SSRC: track.SSRC(), PayloadType: pt},
		}},
	})

	send := NewSender(track, trans)
	return send, nil
}

// Subscribe to a router
func (p *Peer) Subscribe(router *Router) error {
	log.Infof("Subscribing to router %v", router)

	// Create sender track on peer we are sending track to
	sender, err := p.NewSender(router.pub.Track())

	if err != nil {
		log.Errorf("Error creating send track")
		return err
	}

	// Attach sender to source
	router.AddSub(p.id, sender)

	return nil
}

// GetRouter for track
func (p *Peer) GetRouter(ssrc uint32) *Router {
	p.routersLock.RLock()
	defer p.routersLock.RUnlock()
	return p.routers[ssrc]
}

// ID of peer
func (p *Peer) ID() string {
	return p.id
}

// GetStats returns string formatted peer stats
func (p *Peer) GetStats() string {
	info := fmt.Sprintf("peer: %s\n", p.id)

	p.routersLock.RLock()
	for ssrc, router := range p.routers {
		info += fmt.Sprintf("router: %d\n", ssrc)

		if len(router.subs) < 6 {
			for pid := range router.subs {
				info += fmt.Sprintf("sub: %s\n", pid)
			}
			info += "\n"
		} else {
			info += fmt.Sprintf("subs: %d\n\n", len(router.subs))
		}
	}
	p.routersLock.RUnlock()
	return info
}

// Close peer
func (p *Peer) Close() error {
	p.routersLock.Lock()
	for _, router := range p.routers {
		router.Close()
	}
	p.routersLock.Unlock()

	if p.onCloseHandler != nil {
		p.onCloseHandler()
	}

	return p.pc.Close()
}

func (p *Peer) sendRTCP(recv Receiver) {
	// TODO: stop on close
	for {
		pkt, err := recv.ReadRTCP()
		if err != nil {
			// TODO: do something
			log.Errorf("Error reading RTCP %s", err)
			continue
		}

		log.Tracef("sendRTCP %v", pkt)
		p.pc.WriteRTCP([]rtcp.Packet{pkt})
	}
}
