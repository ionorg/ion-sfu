package sfu

import (
	"github.com/pion/ion-sfu/pkg/relay"
	"github.com/pion/webrtc/v3"
)

type RelayPeer struct {
	peer    *relay.Peer
	session Session
	router  Router
}

func NewRelayPeer(peer *relay.Peer, session Session, config *WebRTCTransportConfig) *RelayPeer {
	r := newRouter(peer.ID(), session, config)
	r.SetRTCPWriter(peer.WriteRTCP)

	peer.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver, meta *relay.TrackMeta) {
		if recv, pub := r.AddReceiver(receiver, track); pub {
			session.Publish(r, recv)
		}
	})

	return &RelayPeer{
		peer:    peer,
		session: session,
		router:  r,
	}
}

func (r *RelayPeer) GetRouter() Router {
	return r.router
}

func (r *RelayPeer) ID() string {
	return r.peer.ID()
}
