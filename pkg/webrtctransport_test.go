package sfu

import (
	"context"
	"math/rand"
	"testing"
	"time"

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

func signalPeer(session *Session, remote *webrtc.PeerConnection) (*WebRTCTransport, error) {
	offer, err := remote.CreateOffer(nil)
	if err != nil {
		return nil, err
	}
	if err = remote.SetLocalDescription(offer); err != nil {
		return nil, err
	}
	gatherComplete := webrtc.GatheringCompletePromise(remote)

	peer, err := NewWebRTCTransport(session, offer)
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

func waitForRouter(transport Transport, ssrc uint32) chan struct{} {
	done := make(chan struct{})
	go func() {
		for {
			if transport.GetRouter(ssrc) != nil {
				close(done)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	return done
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
	session := NewSession(1)
	peer, err := signalPeer(session, remote)
	assert.NoError(t, err)
	assert.Equal(t, peer.id, peer.ID())

	done := waitForRouter(peer, track.SSRC())
	sendRTPUntilDone(done, t, []*webrtc.Track{track})
}

func TestNewPeerCallsOnRouterWithAudioTrackRouter(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	remote, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)

	track, err := remote.NewTrack(webrtc.DefaultPayloadTypeOpus, rand.Uint32(), "audio", "pion")
	assert.NoError(t, err)

	_, err = remote.AddTrack(track)
	assert.NoError(t, err)

	// Create sfu peer for remote A and complete signaling
	session := NewSession(1)
	peer, err := signalPeer(session, remote)
	assert.NoError(t, err)
	assert.Equal(t, peer.id, peer.ID())

	done := waitForRouter(peer, track.SSRC())
	sendRTPUntilDone(done, t, []*webrtc.Track{track})
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

	session := NewSession(1)

	// Setup remote <-> peer for a
	peerA, err := signalPeer(session, remoteA)
	assert.NoError(t, err)

	// Add a pub track for remote B
	trackB, err := remoteB.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remoteB.AddTrack(trackB)
	assert.NoError(t, err)

	trackADone := waitForRouter(peerA, trackA.SSRC())
	sendRTPUntilDone(trackADone, t, []*webrtc.Track{trackA})

	// Subscribe b to a
	offer, err := remoteB.CreateOffer(nil)
	assert.NoError(t, err)

	err = remoteB.SetLocalDescription(offer)
	assert.NoError(t, err)
	gatherComplete := webrtc.GatheringCompletePromise(remoteB)

	peerB, err := NewWebRTCTransport(session, offer)
	assert.NoError(t, err)

	// Subscribe to remoteA track
	sender, err := peerB.NewSender(trackA)
	assert.NoError(t, err)
	router := peerA.GetRouter(trackA.SSRC())
	assert.NotNil(t, router)
	router.AddSender(peerB.ID(), sender)

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

	trackBDone := waitForRouter(peerB, trackB.SSRC())
	sendRTPUntilDone(trackBDone, t, []*webrtc.Track{trackB})
}

func TestPeerPairRemoteAGetsOnTrackWhenRemoteBJoinsWithPub(t *testing.T) {
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

	session := NewSession(1)

	// Setup remote <-> peer for a
	peerA, err := signalPeer(session, remoteA)
	assert.NoError(t, err)

	onTrackA := waitForRouter(peerA, trackA.SSRC())
	sendRTPUntilDone(onTrackA, t, []*webrtc.Track{trackA})

	// Add a pub track for remote B
	trackB, err := remoteB.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remoteB.AddTrack(trackB)
	assert.NoError(t, err)

	remoteAOnTrackFired, remoteAOnTrackFiredFunc := context.WithCancel(context.Background())
	remoteA.OnTrack(func(*webrtc.Track, *webrtc.RTPReceiver) {
		remoteAOnTrackFiredFunc()
	})

	// Setup remote <-> peer for a
	peerB, err := signalPeer(session, remoteB)
	assert.NoError(t, err)

	onTrackB := waitForRouter(peerB, trackB.SSRC())
	sendRTPUntilDone(onTrackB, t, []*webrtc.Track{trackB})

	// Subscribe to remoteB track
	sender, err := peerA.NewSender(trackB)

	assert.NoError(t, err)
	router := peerB.GetRouter(trackB.SSRC())
	assert.NotNil(t, router)
	router.AddSender(peerA.ID(), sender)

	// Renegotiate
	offer, err := peerA.CreateOffer()
	assert.NoError(t, err)
	err = peerA.SetLocalDescription(offer)
	assert.NoError(t, err)
	gatherComplete := webrtc.GatheringCompletePromise(peerA.pc)
	<-gatherComplete
	err = remoteA.SetRemoteDescription(*peerA.pc.LocalDescription())
	assert.NoError(t, err)
	answer, err := remoteA.CreateAnswer(nil)
	assert.NoError(t, err)
	err = remoteA.SetLocalDescription(answer)
	assert.NoError(t, err)
	err = peerA.SetRemoteDescription(*remoteA.LocalDescription())
	assert.NoError(t, err)

	sendRTPUntilDone(remoteAOnTrackFired.Done(), t, []*webrtc.Track{trackA, trackB})
}

func TestEventHandlers(t *testing.T) {
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

	session := NewSession(1)

	// Setup remote <-> peer for a
	peerA, err := signalPeer(session, remoteA)
	assert.NoError(t, err)

	peerAOnTrackFired, peerAOnTrackFiredFunc := context.WithCancel(context.Background())
	peerAConnectedFired, peerAConnectedFiredFunc := context.WithCancel(context.Background())
	peerA.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		peerAOnTrackFiredFunc()
	})
	peerA.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			peerAConnectedFiredFunc()
		}
	})

	// Add a pub track for remote B
	trackB, err := remoteB.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remoteB.AddTrack(trackB)
	assert.NoError(t, err)

	trackADone := waitForRouter(peerA, trackA.SSRC())
	sendRTPUntilDone(trackADone, t, []*webrtc.Track{trackA})

	// Subscribe b to a
	offer, err := remoteB.CreateOffer(nil)
	assert.NoError(t, err)

	err = remoteB.SetLocalDescription(offer)
	assert.NoError(t, err)
	gatherComplete := webrtc.GatheringCompletePromise(remoteB)

	peerB, err := NewWebRTCTransport(session, offer)
	assert.NoError(t, err)

	// Subscribe to remoteA track
	sender, err := peerB.NewSender(trackA)
	assert.NoError(t, err)
	router := peerA.GetRouter(trackA.SSRC())
	assert.NotNil(t, router)
	router.AddSender(peerB.ID(), sender)

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

	sendRTPUntilDone(peerAOnTrackFired.Done(), t, []*webrtc.Track{trackB})
	<-peerAConnectedFired.Done()
}
