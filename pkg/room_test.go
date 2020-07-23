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

	// Close peer so they are removed from room
	peer.Close()

	assert.Len(t, room.peers, 0)

	<-onCloseFired.Done()
}
