package sfu

import (
	"context"
	"math/rand"
	"testing"

	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

func TestRoom(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	remote, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)

	// Add a pub track for remote A
	track, err := remote.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remote.AddTrack(track)
	assert.NoError(t, err)

	// Setup remote <-> peer for a
	offer, err := remote.CreateOffer(nil)
	assert.NoError(t, err)
	gatherComplete := webrtc.GatheringCompletePromise(remote)
	err = remote.SetLocalDescription(offer)
	assert.NoError(t, err)
	<-gatherComplete
	peer, err := NewPeer(*remote.LocalDescription())
	assert.NoError(t, err)

	room := NewRoom("room")
	onCloseFired, onCloseFiredFunc := context.WithCancel(context.Background())
	room.OnClose(func() {
		onCloseFiredFunc()
	})

	room.AddPeer(peer)

	assert.Equal(t, peer, room.peers[peer.id])
	assert.Len(t, room.peers, 1)

	stats := room.stats()

	assert.Contains(t, stats, peer.id)

	// Close peer so they are removed from room
	peer.Close()

	assert.Len(t, room.peers, 0)

	<-onCloseFired.Done()
}

func TestMultiPeerRoom(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	remoteA, remoteB, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	// Add a pub track for remote A
	trackA, err := remoteA.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remoteA.AddTrack(trackA)
	assert.NoError(t, err)

	// Setup remote <-> peer for a
	peerA, err := signalPeer(remoteA)
	assert.NoError(t, err)

	// Add a pub track for remote B
	trackB, err := remoteB.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remoteB.AddTrack(trackB)
	assert.NoError(t, err)

	// Setup remote <-> peer for b
	peerB, err := signalPeer(remoteB)
	assert.NoError(t, err)

	room := NewRoom("room")

	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())
	room.OnClose(func() {
		onReadRTPFiredFunc()
	})

	onNegotationNeededFired, onNegotationNeededFiredFunc := context.WithCancel(context.Background())
	peerA.OnNegotiationNeeded(func() {
		onNegotationNeededFiredFunc()
	})

	room.AddPeer(peerA)

	cacheFn := peerA.onRouterHander
	peerA.OnRouter(func(router *Router) {
		cacheFn(router)
		assert.Len(t, peerA.routers, 1)

		room.AddPeer(peerB)

		cacheFn = peerB.onRouterHander
		peerB.OnRouter(func(router *Router) {
			cacheFn(router)
			assert.Len(t, peerB.routers, 1)

			assert.Len(t, peerA.routers[trackA.SSRC()].subs, 1)
			assert.Equal(t, trackA.SSRC(), peerA.routers[trackA.SSRC()].subs[peerB.id].track.SSRC())
			assert.Len(t, peerB.routers[trackB.SSRC()].subs, 1)
			assert.Equal(t, trackB.SSRC(), peerB.routers[trackB.SSRC()].subs[peerA.id].track.SSRC())

			peerA.Close()
			peerB.Close()
		})

		sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{trackB})
	})

	sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{trackA})
	<-onNegotationNeededFired.Done()
}
