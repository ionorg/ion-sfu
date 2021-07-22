package sfu

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pion/rtcp"

	"github.com/pion/ion-sfu/pkg/relay"
	"github.com/pion/webrtc/v3"
)

type RelayPeer struct {
	mu sync.Mutex

	peer       *relay.Peer
	session    Session
	router     Router
	tracks     []publisherTracks
	relayPeers []*relay.Peer
	config     *WebRTCTransportConfig
}

func NewRelayPeer(peer *relay.Peer, session Session, config *WebRTCTransportConfig) *RelayPeer {
	r := newRouter(peer.ID(), session, config)
	r.SetRTCPWriter(peer.WriteRTCP)

	rp := &RelayPeer{
		peer:    peer,
		session: session,
		router:  r,
	}

	peer.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver, meta *relay.TrackMeta) {
		if recv, pub := r.AddReceiver(receiver, track); pub {
			session.Publish(r, recv)
			rp.mu.Lock()
			rp.tracks = append(rp.tracks, publisherTracks{track, recv, true})
			for _, lrp := range rp.relayPeers {
				if err := rp.createRelayTrack(track, recv, lrp); err != nil {
					Logger.V(1).Error(err, "Creating relay track.", "peer_id", peer.ID())
				}
			}
			rp.mu.Unlock()
		} else {
			rp.mu.Lock()
			rp.tracks = append(rp.tracks, publisherTracks{track, recv, false})
			rp.mu.Unlock()
		}
	})

	return rp
}

func (r *RelayPeer) GetRouter() Router {
	return r.router
}

func (r *RelayPeer) ID() string {
	return r.peer.ID()
}

func (r *RelayPeer) Relay(signalFn func(meta relay.PeerMeta, signal []byte) ([]byte, error)) (*relay.Peer, error) {
	rp, err := relay.NewPeer(relay.PeerMeta{
		PeerID:    r.peer.ID(),
		SessionID: r.session.ID(),
	}, &relay.PeerConfig{
		SettingEngine: r.config.Setting,
		ICEServers:    r.config.Configuration.ICEServers,
		Logger:        Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}

	rp.OnReady(func() {
		for _, lbl := range r.session.GetDataChannelLabels() {
			if _, err := rp.CreateDataChannel(lbl); err != nil {
				Logger.V(1).Error(err, "Creating data channels.", "peer_id", r.ID())
			}
		}

		r.mu.Lock()
		for _, tp := range r.tracks {
			if !tp.clientRelay {
				// simulcast will just relay client track for now
				continue
			}
			if err = r.createRelayTrack(tp.track, tp.receiver, rp); err != nil {
				Logger.V(1).Error(err, "Creating relay track.", "peer_id", r.ID())
			}
		}
		r.relayPeers = append(r.relayPeers, rp)
		r.mu.Unlock()
		go r.relayReports(rp)
	})

	if err = rp.Offer(signalFn); err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}

	return rp, nil
}

func (r *RelayPeer) createRelayTrack(track *webrtc.TrackRemote, receiver Receiver, rp *relay.Peer) error {
	codec := track.Codec()
	downTrack, err := NewDownTrack(webrtc.RTPCodecCapability{
		MimeType:     codec.MimeType,
		ClockRate:    codec.ClockRate,
		Channels:     codec.Channels,
		SDPFmtpLine:  codec.SDPFmtpLine,
		RTCPFeedback: []webrtc.RTCPFeedback{{"nack", ""}, {"nack", "pli"}},
	}, receiver, r.config.BufferFactory, r.ID(), r.config.Router.MaxPacketTrack)
	if err != nil {
		Logger.V(1).Error(err, "Create Relay downtrack err", "peer_id", r.ID())
		return err
	}

	sdr, err := rp.AddTrack(receiver.(*WebRTCReceiver).receiver, track, downTrack)
	if err != nil {
		Logger.V(1).Error(err, "Relaying track.", "peer_id", r.ID())
		return fmt.Errorf("relay: %w", err)
	}

	downTrack.OnCloseHandler(func() {
		if err = sdr.Stop(); err != nil {
			Logger.V(1).Error(err, "Stopping relay sender.", "peer_id", r.ID())
		}
	})

	receiver.AddDownTrack(downTrack, true)
	return nil
}

func (r *RelayPeer) relayReports(rp *relay.Peer) {
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