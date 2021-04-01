package sfu

import (
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtcp"

	"github.com/pion/ion-sfu/pkg/relay"

	"github.com/pion/webrtc/v3"
)

type Publisher struct {
	id string
	pc *webrtc.PeerConnection

	router     Router
	session    *Session
	relayPeer  *relay.Peer
	candidates []webrtc.ICECandidateInit

	onICEConnectionStateChangeHandler atomic.Value // func(webrtc.ICEConnectionState)

	closeOnce sync.Once
}

// NewPublisher creates a new Publisher
func NewPublisher(session *Session, id string, relay bool, cfg WebRTCTransportConfig) (*Publisher, error) {
	me, err := getPublisherMediaEngine()
	if err != nil {
		Logger.Error(err, "NewPeer error", "peer_id", id)
		return nil, errPeerConnectionInitFailed
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(cfg.setting))
	pc, err := api.NewPeerConnection(cfg.configuration)

	if err != nil {
		Logger.Error(err, "NewPeer error", "peer_id", id)
		return nil, errPeerConnectionInitFailed
	}

	p := &Publisher{
		id:      id,
		pc:      pc,
		router:  newRouter(id, pc, session, cfg.router),
		session: session,
	}

	var relayReports sync.Once
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
		}

		if relay && cfg.relay != nil && pub {
			codec := track.Codec()
			downTrack, err := NewDownTrack(webrtc.RTPCodecCapability{
				MimeType:     codec.MimeType,
				ClockRate:    codec.ClockRate,
				Channels:     codec.Channels,
				SDPFmtpLine:  codec.SDPFmtpLine,
				RTCPFeedback: []webrtc.RTCPFeedback{{"goog-remb", ""}, {"nack", ""}, {"nack", "pli"}},
			}, r, session.bufferFactory, id, cfg.router.MaxPacketTrack)
			if err != nil {
				Logger.V(1).Error(err, "Create relay downtrack err", "peer_id", id)
				return
			}
			rr, sdr, err := cfg.relay.Send(session.id, id, receiver, downTrack)
			if err != nil {
				Logger.V(1).Error(err, "Relay err", "peer_id", id)
				return
			}

			if p.relayPeer == nil {
				relayReports.Do(func() {
					p.relayPeer = rr
					go p.relayReports()
				})
			}

			downTrack.OnCloseHandler(func() {
				if err := sdr.Stop(); err != nil {
					Logger.V(1).Error(err, "Relay sender close err", "peer_id", id)
				}
			})

			r.AddDownTrack(downTrack, true)
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

	if relay && cfg.relay != nil {
		if err = cfg.relay.AddDataChannels(session.id, id, session.getDataChannelLabels()); err != nil {
			Logger.Error(err, "Add relaying data channels error")
		}
	}
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

// GetRouter returns router with mediaSSRC
func (p *Publisher) GetRouter() Router {
	return p.router
}

// Close peer
func (p *Publisher) Close() {
	p.closeOnce.Do(func() {
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

// AddICECandidate to peer connection
func (p *Publisher) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	if p.pc.RemoteDescription() != nil {
		return p.pc.AddICECandidate(candidate)
	}
	p.candidates = append(p.candidates, candidate)
	return nil
}

func (p *Publisher) relayReports() {
	for {
		time.Sleep(5 * time.Second)

		var r []rtcp.Packet
		for _, t := range p.relayPeer.LocalTracks() {
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

		if err := p.relayPeer.WriteRTCP(r); err != nil {
			if err == io.EOF || err == io.ErrClosedPipe {
				return
			}
			Logger.Error(err, "Sending downtrack reports err")
		}
	}
}
