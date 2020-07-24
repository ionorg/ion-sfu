package sfu

import (
	"context"
	"math/rand"
	"testing"

	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

// newPair creates two new peer connections (an offerer and an answerer) using
// the api.
func newPair(cfg webrtc.Configuration, api *webrtc.API) (pcOffer *webrtc.PeerConnection, pcAnswer *webrtc.PeerConnection, err error) {
	pca, err := api.NewPeerConnection(cfg)
	if err != nil {
		return nil, nil, err
	}

	pcb, err := api.NewPeerConnection(cfg)
	if err != nil {
		return nil, nil, err
	}

	return pca, pcb, nil
}

func signalPeer(remote *webrtc.PeerConnection) (*Peer, error) {
	offer, err := remote.CreateOffer(nil)
	if err != nil {
		return nil, err
	}
	if err = remote.SetLocalDescription(offer); err != nil {
		return nil, err
	}
	gatherComplete := webrtc.GatheringCompletePromise(remote)

	peer, err := NewPeer(offer)
	if err != nil {
		return nil, err
	}
	<-gatherComplete

	if err = peer.SetRemoteDescription(*remote.LocalDescription()); err != nil {
		return nil, err
	}

	answer, err := peer.CreateAnswer()
	if err != nil {
		return nil, err
	}
	if err = peer.SetLocalDescription(answer); err != nil {
		return nil, err
	}
	err = remote.SetRemoteDescription(answer)
	if err != nil {
		return nil, err
	}
	return peer, nil
}

func TestNewPeerCallsOnRouterWithVideoTrackRouter(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	remote, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)

	track, err := remote.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)

	_, err = remote.AddTrack(track)
	assert.NoError(t, err)

	// Create sfu peer for remote A and complete signaling
	peer, err := signalPeer(remote)
	assert.NoError(t, err)

	onRouterFired, onRouterFiredFunc := context.WithCancel(context.Background())
	peer.OnRouter(func(r *Router) {
		assert.Equal(t, track.SSRC(), r.pub.Track().SSRC())
		onRouterFiredFunc()
	})

	sendRTPUntilDone(onRouterFired.Done(), t, []*webrtc.Track{track})
}

func TestPeerPairRemoteBGetsOnTrack(t *testing.T) {
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

	remoteBOnTrackFired, remoteBOnTrackFiredFunc := context.WithCancel(context.Background())
	remoteB.OnTrack(func(*webrtc.Track, *webrtc.RTPReceiver) {
		remoteBOnTrackFiredFunc()
	})

	// Subscribe b to a
	// peerAOnRouterCalled := make(chan bool)
	peerA.OnRouter(func(r *Router) {
		// Setup remote <-> peer for b
		offer, err := remoteB.CreateOffer(nil)
		assert.NoError(t, err)

		err = remoteB.SetLocalDescription(offer)
		assert.NoError(t, err)
		gatherComplete := webrtc.GatheringCompletePromise(remoteB)

		peerB, err := NewPeer(offer)
		assert.NoError(t, err)

		// Subscribe to remoteA track
		err = peerB.Subscribe(r)
		assert.NoError(t, err)

		<-gatherComplete

		// Finish signaling
		err = peerB.SetRemoteDescription(*remoteB.LocalDescription())
		assert.NoError(t, err)
		answer, err := peerB.CreateAnswer()
		assert.NoError(t, err)
		err = peerB.SetLocalDescription(answer)
		assert.NoError(t, err)
		err = remoteB.SetRemoteDescription(*peerB.pc.LocalDescription())
		assert.NoError(t, err)
	})

	sendRTPUntilDone(remoteBOnTrackFired.Done(), t, []*webrtc.Track{trackA, trackB})
}

// func TestPeerPairRemoteAGetsOnTrackWhenRemoteBJoinsWithPub(t *testing.T) {
// 	me := webrtc.MediaEngine{}
// 	me.RegisterDefaultCodecs()
// 	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
// 	remoteA, remoteB, err := newPair(webrtc.Configuration{}, api)
// 	assert.NoError(t, err)

// 	// Add a pub track for remote A
// 	trackA, err := remoteA.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
// 	assert.NoError(t, err)
// 	_, err = remoteA.AddTrack(trackA)
// 	assert.NoError(t, err)

// 	// Add a pub track for remote B
// 	trackB, err := remoteB.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
// 	assert.NoError(t, err)
// 	_, err = remoteB.AddTrack(trackB)
// 	assert.NoError(t, err)

// 	remoteAOnTrackFired, remoteAOnTrackFiredFunc := context.WithCancel(context.Background())
// 	remoteA.OnTrack(func(*webrtc.Track, *webrtc.RTPReceiver) {
// 		remoteAOnTrackFiredFunc()
// 	})

// 	remoteB.OnTrack(func(*webrtc.Track, *webrtc.RTPReceiver) {
// 		remoteAOnTrackFiredFunc()
// 	})

// 	// Setup remote <-> peer for a
// 	offerA, err := remoteA.CreateOffer(nil)
// 	assert.NoError(t, err)
// 	gatherComplete := webrtc.GatheringCompletePromise(remoteA)
// 	err = remoteA.SetLocalDescription(offerA)
// 	assert.NoError(t, err)
// 	<-gatherComplete
// 	peerA, err := NewPeer(*remoteA.LocalDescription())
// 	assert.NoError(t, err)

// 	// Setup remote <-> peer for b
// 	offerB, err := remoteB.CreateOffer(nil)
// 	assert.NoError(t, err)
// 	gatherComplete = webrtc.GatheringCompletePromise(remoteA)
// 	err = remoteB.SetLocalDescription(offerB)
// 	assert.NoError(t, err)
// 	<-gatherComplete
// 	peerB, err := NewPeer(*remoteB.LocalDescription())
// 	assert.NoError(t, err)

// 	// Subscribe b to a
// 	peerAOnRouterCalled := make(chan bool)
// 	peerA.OnRouter(func(r *Router) {
// 		err = peerB.Subscribe(r)
// 		assert.NoError(t, err)
// 		close(peerAOnRouterCalled)
// 	})

// 	// Subscribe a to b
// 	peerBOnRouterCalled := make(chan bool)
// 	peerB.OnRouter(func(r *Router) {
// 		err = peerA.Subscribe(r)
// 		assert.NoError(t, err)
// 		close(peerBOnRouterCalled)
// 	})

// 	// Resignal for remote a
// 	peerANegotiationHandlerCalled := make(chan bool)
// 	peerA.OnNegotiationNeeded(func() {
// 		offer, err := peerA.CreateOffer()
// 		assert.NoError(t, err)
// 		gatherComplete := webrtc.GatheringCompletePromise(remoteA)
// 		err = peerA.SetLocalDescription(offer)
// 		assert.NoError(t, err)
// 		<-gatherComplete
// 		err = remoteA.SetRemoteDescription(*peerA.pc.LocalDescription())
// 		assert.NoError(t, err)
// 		answer, err := remoteA.CreateAnswer(nil)
// 		assert.NoError(t, err)
// 		err = peerA.SetRemoteDescription(answer)
// 		assert.NoError(t, err)
// 		close(peerANegotiationHandlerCalled)
// 		remoteAOnTrackFiredFunc()
// 	})

// 	// Resignal for remote b
// 	peerBNegotiationHandlerCalled := make(chan bool)
// 	peerB.OnNegotiationNeeded(func() {
// 		offer, err := peerB.CreateOffer()
// 		assert.NoError(t, err)
// 		gatherComplete := webrtc.GatheringCompletePromise(remoteA)
// 		err = peerB.SetLocalDescription(offer)
// 		assert.NoError(t, err)
// 		<-gatherComplete
// 		err = remoteB.SetRemoteDescription(*peerB.pc.LocalDescription())
// 		assert.NoError(t, err)
// 		answer, err := remoteB.CreateAnswer(nil)
// 		assert.NoError(t, err)
// 		err = peerB.SetRemoteDescription(answer)
// 		assert.NoError(t, err)
// 		close(peerBNegotiationHandlerCalled)
// 	})

// 	// Complete intial signaling for A
// 	err = peerA.SetRemoteDescription(*remoteA.LocalDescription())
// 	assert.NoError(t, err)
// 	answer, err := peerA.CreateAnswer()
// 	assert.NoError(t, err)
// 	err = peerA.SetLocalDescription(answer)
// 	assert.NoError(t, err)
// 	err = remoteA.SetRemoteDescription(answer)
// 	assert.NoError(t, err)

// 	// Complete intial signaling for B
// 	err = peerB.SetRemoteDescription(*remoteB.LocalDescription())
// 	assert.NoError(t, err)
// 	answer, err = peerB.CreateAnswer()
// 	assert.NoError(t, err)
// 	err = peerB.SetLocalDescription(answer)
// 	assert.NoError(t, err)
// 	err = remoteB.SetRemoteDescription(answer)
// 	assert.NoError(t, err)

// 	sendRTPUntilDone(remoteAOnTrackFired.Done(), t, []*webrtc.Track{trackA, trackB})
// 	<-peerAOnRouterCalled
// 	<-peerBOnRouterCalled
// 	<-peerANegotiationHandlerCalled
// 	<-peerBNegotiationHandlerCalled
// }
