package sfu

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ion-sfu/pkg/relay"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

type Publisher struct {
	mu  sync.Mutex
	id  string
	pc  *webrtc.PeerConnection
	cfg *WebRTCTransportConfig

	router     Router
	session    Session
	tracks     []publisherTracks
	relayPeer  []*relay.Peer
	candidates []webrtc.ICECandidateInit

	onICEConnectionStateChangeHandler atomic.Value // func(webrtc.ICEConnectionState)

	closeOnce sync.Once
}

type publisherTracks struct {
	track    *webrtc.TrackRemote
	receiver Receiver
	// This will be used in the future for tracks that will be relayed as clients or servers
	// This is for SVC and Simulcast where you will be able to chose if the relayed peer just
	// want a single track (for recording/ processing) or get all the tracks (for load balancing)
	clientRelay bool
}

// NewPublisher creates a new Publisher
func NewPublisher(id string, session Session, cfg *WebRTCTransportConfig) (*Publisher, error) {
	me, err := getPublisherMediaEngine()
	if err != nil {
		Logger.Error(err, "NewPeer error", "peer_id", id)
		return nil, errPeerConnectionInitFailed
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(cfg.Setting))
	pc, err := api.NewPeerConnection(cfg.Configuration)

	if err != nil {
		Logger.Error(err, "NewPeer error", "peer_id", id)
		return nil, errPeerConnectionInitFailed
	}

	p := &Publisher{
		id:      id,
		pc:      pc,
		cfg:     cfg,
		router:  newRouter(id, pc, session, cfg),
		session: session,
	}

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		Logger.V(1).Info("Peer got remote track id",
			"peer_id", p.id,
			"track_id", track.ID(),
			"mediaSSRC", track.SSRC(),
			"rid", track.RID(),
			"stream_id", track.StreamID(),
		)

		r, pub := p.router.AddReceiver(receiver, track)
		if pub {
			p.session.Publish(p.router, r)
			p.mu.Lock()
			p.tracks = append(p.tracks, publisherTracks{track, r, true})
			for _, rp := range p.relayPeer {
				if err = p.createRelayTrack(track, r, rp); err != nil {
					Logger.V(1).Error(err, "Creating relay track.", "peer_id", p.id)
				}
			}
			p.mu.Unlock()
		} else {
			p.mu.Lock()
			p.tracks = append(p.tracks, publisherTracks{track, r, false})
			p.mu.Unlock()
		}
	})

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() == APIChannelLabel {
			// terminate api data channel
			return
		}
		p.session.AddDatachannel(id, dc)
	})

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		Logger.V(1).Info("ice connection status", "state", connectionState)
		switch connectionState {
		case webrtc.ICEConnectionStateFailed:
			fallthrough
		case webrtc.ICEConnectionStateClosed:
			Logger.V(1).Info("webrtc ice closed", "peer_id", p.id)
			p.Close()
		}

		if handler, ok := p.onICEConnectionStateChangeHandler.Load().(func(webrtc.ICEConnectionState)); ok && handler != nil {
			handler(connectionState)
		}
	})

	return p, nil
}

func (p *Publisher) Answer(offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return webrtc.SessionDescription{}, err
	}

	for _, c := range p.candidates {
		if err := p.pc.AddICECandidate(c); err != nil {
			Logger.Error(err, "Add publisher ice candidate to peer err", "peer_id", p.id)
		}
	}
	p.candidates = nil

	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	return answer, nil
}

// GetRouter returns Router with mediaSSRC
func (p *Publisher) GetRouter() Router {
	return p.router
}

// Close peer
func (p *Publisher) Close() {
	p.closeOnce.Do(func() {
		if len(p.relayPeer) > 0 {
			p.mu.Lock()
			for _, rp := range p.relayPeer {
				if err := rp.Close(); err != nil {
					Logger.Error(err, "Closing relay peer transport.")
				}
			}
			p.mu.Unlock()
		}
		p.router.Stop()
		if err := p.pc.Close(); err != nil {
			Logger.Error(err, "webrtc transport close err")
		}
	})
}

// OnICECandidate handler
func (p *Publisher) OnICECandidate(f func(c *webrtc.ICECandidate)) {
	p.pc.OnICECandidate(f)
}

func (p *Publisher) OnICEConnectionStateChange(f func(connectionState webrtc.ICEConnectionState)) {
	p.onICEConnectionStateChangeHandler.Store(f)
}

func (p *Publisher) SignalingState() webrtc.SignalingState {
	return p.pc.SignalingState()
}

func (p *Publisher) PeerConnection() *webrtc.PeerConnection {
	return p.pc
}

func (p *Publisher) Relay(ice []webrtc.ICEServer) (*relay.Peer, error) {
	rp, err := relay.NewPeer(relay.PeerMeta{
		PeerID:    p.id,
		SessionID: p.session.ID(),
	}, &relay.PeerConfig{
		SettingEngine: p.cfg.Setting,
		ICEServers:    ice,
		Logger:        Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}

	rp.OnReady(func() {
		for _, lbl := range p.session.GetDataChannelLabels() {
			if _, err := rp.CreateDataChannel(lbl); err != nil {
				Logger.V(1).Error(err, "Creating data channels.", "peer_id", p.id)
			}
		}

		p.mu.Lock()
		for _, tp := range p.tracks {
			if !tp.clientRelay {
				// simulcast will just relay client track for now
				continue
			}
			if err = p.createRelayTrack(tp.track, tp.receiver, rp); err != nil {
				Logger.V(1).Error(err, "Creating relay track.", "peer_id", p.id)
			}
		}
		p.relayPeer = append(p.relayPeer, rp)

		go p.relayReports(rp)
		p.mu.Unlock()
	})

	if err = rp.Offer(p.cfg.Relay); err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}

	return rp, nil
}

func (p *Publisher) Tracks() []*webrtc.TrackRemote {
	p.mu.Lock()
	defer p.mu.Unlock()

	tracks := make([]*webrtc.TrackRemote, len(p.tracks))
	for idx, track := range p.tracks {
		tracks[idx] = track.track
	}
	return tracks
}

// AddICECandidate to peer connection
func (p *Publisher) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	if p.pc.RemoteDescription() != nil {
		return p.pc.AddICECandidate(candidate)
	}
	p.candidates = append(p.candidates, candidate)
	return nil
}

func (p *Publisher) createRelayTrack(track *webrtc.TrackRemote, receiver Receiver, rp *relay.Peer) error {
	codec := track.Codec()
	downTrack, err := NewDownTrack(webrtc.RTPCodecCapability{
		MimeType:     codec.MimeType,
		ClockRate:    codec.ClockRate,
		Channels:     codec.Channels,
		SDPFmtpLine:  codec.SDPFmtpLine,
		RTCPFeedback: []webrtc.RTCPFeedback{{"nack", ""}, {"nack", "pli"}},
	}, receiver, p.cfg.BufferFactory, p.id, p.cfg.Router.MaxPacketTrack)
	if err != nil {
		Logger.V(1).Error(err, "Create Relay downtrack err", "peer_id", p.id)
		return err
	}

	sdr, err := rp.AddTrack(receiver.(*WebRTCReceiver).receiver, track, downTrack)
	if err != nil {
		Logger.V(1).Error(err, "Relaying track.", "peer_id", p.id)
		return fmt.Errorf("relay: %w", err)
	}

	downTrack.OnCloseHandler(func() {
		if err = sdr.Stop(); err != nil {
			Logger.V(1).Error(err, "Stopping relay sender.", "peer_id", p.id)
		}
	})

	receiver.AddDownTrack(downTrack, true)
	return nil
}

func (p *Publisher) relayReports(rp *relay.Peer) {
	for {
		time.Sleep(5 * time.Second)

		var r []rtcp.Packet
		for _, t := range rp.LocalTracks() {
			if dt, ok := t.(*DownTrack); ok {
				if !dt.bound.get() {
					continue
				}
				r = append(r, dt.CreateSenderReport())
			}
		}

		if len(r) == 0 {
			continue
		}

		if err := rp.WriteRTCP(r); err != nil {
			if err == io.EOF || err == io.ErrClosedPipe {
				return
			}
			Logger.Error(err, "Sending downtrack reports err")
		}
	}
}
