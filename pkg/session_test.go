package sfu

import (
	"context"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

func createPeer(t *testing.T, api *webrtc.API) (*Peer, *webrtc.PeerConnection, *webrtc.Track, error) {
	remote, err := api.NewPeerConnection(cfg)
	if err != nil {
		return nil, nil, nil, err
	}

	// Add a pub track for remote
	track, err := remote.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remote.AddTrack(track)
	if err != nil {
		return nil, nil, nil, err
	}

	// Setup remote <-> peer for a
	peer, err := signalPeer(remote)
	if err != nil {
		return nil, nil, nil, err
	}

	remoteCloseFired, remoteCloseFiredFunc := context.WithCancel(context.Background())
	remote.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		switch connectionState {
		case webrtc.ICEConnectionStateClosed:
			remoteCloseFiredFunc()
		}
	})
	go sendRTPUntilDone(remoteCloseFired.Done(), t, []*webrtc.Track{track})

	return peer, remote, track, nil
}

func TestSession(t *testing.T) {
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

	session := NewSession("session")
	onCloseFired, onCloseFiredFunc := context.WithCancel(context.Background())
	session.OnClose(func() {
		onCloseFiredFunc()
	})

	session.AddTransport(peer)

	assert.Equal(t, peer, session.transports[peer.id])
	assert.Len(t, session.transports, 1)

	stats := session.stats()

	assert.Contains(t, stats, peer.id)

	// Close peer so they are removed from session
	peer.Close()

	assert.Len(t, session.transports, 0)

	<-onCloseFired.Done()
}

func TestMultiPeerSession(t *testing.T) {
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

	session := NewSession("session")

	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())
	session.OnClose(func() {
		onReadRTPFiredFunc()
	})

	onNegotationNeededFired, onNegotationNeededFiredFunc := context.WithCancel(context.Background())
	peerA.OnNegotiationNeeded(func() {
		onNegotationNeededFiredFunc()
	})

	session.AddTransport(peerA)

	cacheFn := peerA.onRouterHander
	peerA.OnRouter(func(router *Router) {
		cacheFn(router)
		assert.Len(t, peerA.routers, 1)

		session.AddTransport(peerB)

		cacheFn = peerB.onRouterHander
		peerB.OnRouter(func(router *Router) {
			cacheFn(router)
			assert.Len(t, peerB.routers, 1)

			assert.Len(t, peerA.routers[trackA.SSRC()].senders, 1)
			assert.Len(t, peerB.routers[trackB.SSRC()].senders, 1)

			peerA.Close()
			assert.Len(t, peerB.routers[trackB.SSRC()].senders, 0)

			peerB.Close()
		})

		sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{trackB})
	})

	sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{trackA})
	<-onNegotationNeededFired.Done()
}

func Test3PeerConcurrrentJoin(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	peerA, _, trackA, err := createPeer(t, api)
	assert.NoError(t, err)

	peerB, _, trackB, err := createPeer(t, api)
	assert.NoError(t, err)

	peerC, _, trackC, err := createPeer(t, api)
	assert.NoError(t, err)

	session := NewSession("session")

	peerAGotTracks := make(chan bool)
	peerA.OnNegotiationNeeded(func() {
		log.Infof("OnNegotiationNeeded A called")
		offer, err := peerA.CreateOffer()
		assert.NoError(t, err)

		desc := sdp.SessionDescription{}
		err = desc.Unmarshal([]byte(offer.SDP))
		assert.NoError(t, err)

		trackBSeen := false
		trackCSeen := false
		for _, md := range desc.MediaDescriptions {
			for _, attr := range md.Attributes {
				switch attr.Key {
				case sdp.AttrKeySSRC:
					split := strings.Split(attr.Value, " ")
					ssrc, err := strconv.ParseUint(split[0], 10, 32)
					assert.NoError(t, err)
					if uint32(ssrc) == trackB.SSRC() {
						trackBSeen = true
					} else if uint32(ssrc) == trackC.SSRC() {
						trackCSeen = true
					}
				}
			}
		}

		if trackBSeen && trackCSeen {
			close(peerAGotTracks)
		}
	})

	peerBGotTracks := make(chan bool)
	peerB.OnNegotiationNeeded(func() {
		log.Infof("OnNegotiationNeeded B called")
		offer, err := peerB.CreateOffer()
		assert.NoError(t, err)

		desc := sdp.SessionDescription{}
		err = desc.Unmarshal([]byte(offer.SDP))
		assert.NoError(t, err)

		trackASeen := false
		trackCSeen := false
		for _, md := range desc.MediaDescriptions {
			for _, attr := range md.Attributes {
				switch attr.Key {
				case sdp.AttrKeySSRC:
					split := strings.Split(attr.Value, " ")
					ssrc, err := strconv.ParseUint(split[0], 10, 32)
					assert.NoError(t, err)
					if uint32(ssrc) == trackA.SSRC() {
						trackASeen = true
					} else if uint32(ssrc) == trackC.SSRC() {
						trackCSeen = true
					}
				}
			}
		}

		if trackASeen && trackCSeen {
			close(peerBGotTracks)
		}
	})

	peerCGotTracks := make(chan bool)
	peerC.OnNegotiationNeeded(func() {
		offer, err := peerC.CreateOffer()
		assert.NoError(t, err)

		desc := sdp.SessionDescription{}
		err = desc.Unmarshal([]byte(offer.SDP))
		assert.NoError(t, err)

		trackASeen := false
		trackBSeen := false
		for _, md := range desc.MediaDescriptions {
			for _, attr := range md.Attributes {
				switch attr.Key {
				case sdp.AttrKeySSRC:
					split := strings.Split(attr.Value, " ")
					ssrc, err := strconv.ParseUint(split[0], 10, 32)
					assert.NoError(t, err)
					if uint32(ssrc) == trackA.SSRC() {
						trackASeen = true
					} else if uint32(ssrc) == trackB.SSRC() {
						trackBSeen = true
					}
				}
			}
		}

		if trackASeen && trackBSeen {
			close(peerCGotTracks)
		}
	})

	session.AddTransport(peerA)
	session.AddTransport(peerB)
	session.AddTransport(peerC)
	<-peerAGotTracks
	<-peerBGotTracks
	<-peerCGotTracks
}

func Test3PeerStaggerJoin(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))

	remoteA, err := api.NewPeerConnection(cfg)
	assert.NoError(t, err)

	// Add a pub track for remote A
	trackA, err := remoteA.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remoteA.AddTrack(trackA)
	assert.NoError(t, err)

	// Setup remote <-> peer for a
	peerA, err := signalPeer(remoteA)
	assert.NoError(t, err)
	session := NewSession("session")

	session.AddTransport(peerA)
	done := make(chan struct{})
	cacheFn := peerA.onRouterHander
	peerA.OnRouter(func(router *Router) {
		cacheFn(router)

		remoteB, err := api.NewPeerConnection(cfg)
		assert.NoError(t, err)
		// Add a pub track for remote B
		trackB, err := remoteB.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
		assert.NoError(t, err)
		_, err = remoteB.AddTrack(trackB)
		assert.NoError(t, err)
		offer, err := remoteB.CreateOffer(nil)
		assert.NoError(t, err)
		err = remoteB.SetLocalDescription(offer)
		assert.NoError(t, err)
		gatherComplete := webrtc.GatheringCompletePromise(remoteB)
		peerB, err := NewPeer(offer)
		session.AddTransport(peerB)
		assert.NoError(t, err)
		<-gatherComplete
		err = peerB.SetRemoteDescription(*remoteB.LocalDescription())
		assert.NoError(t, err)
		answer, err := peerB.CreateAnswer()
		assert.NoError(t, err)
		err = peerB.SetLocalDescription(answer)
		assert.NoError(t, err)
		err = remoteB.SetRemoteDescription(*peerB.pc.LocalDescription())
		assert.NoError(t, err)

		desc := sdp.SessionDescription{}
		err = desc.Unmarshal([]byte(peerB.pc.LocalDescription().SDP))
		assert.NoError(t, err)

		trackASeen := false
		for _, md := range desc.MediaDescriptions {
			for _, attr := range md.Attributes {
				switch attr.Key {
				case sdp.AttrKeySSRC:
					split := strings.Split(attr.Value, " ")
					ssrc, err := strconv.ParseUint(split[0], 10, 32)
					assert.NoError(t, err)
					if uint32(ssrc) == trackA.SSRC() {
						trackASeen = true
					}
				}
			}
		}

		assert.True(t, trackASeen)

		cacheFn := peerB.onRouterHander
		peerB.OnRouter(func(router *Router) {
			cacheFn(router)

			remoteC, err := api.NewPeerConnection(cfg)
			assert.NoError(t, err)
			// Add transceiver to match number of recv tracks
			_, err = remoteC.AddTransceiverFromTrack(trackB)
			assert.NoError(t, err)

			// Add a pub track for remote B
			trackC, err := remoteC.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
			assert.NoError(t, err)
			_, err = remoteC.AddTrack(trackC)
			assert.NoError(t, err)
			offer, err := remoteC.CreateOffer(nil)
			assert.NoError(t, err)
			err = remoteC.SetLocalDescription(offer)
			assert.NoError(t, err)
			gatherComplete := webrtc.GatheringCompletePromise(remoteC)
			peerC, err := NewPeer(offer)
			session.AddTransport(peerC)
			assert.NoError(t, err)
			<-gatherComplete
			err = peerC.SetRemoteDescription(*remoteC.LocalDescription())
			assert.NoError(t, err)
			log.Infof("create answer")
			answer, err := peerC.CreateAnswer()
			assert.NoError(t, err)
			err = peerC.SetLocalDescription(answer)
			assert.NoError(t, err)
			err = remoteC.SetRemoteDescription(*peerC.pc.LocalDescription())
			assert.NoError(t, err)

			desc := sdp.SessionDescription{}
			err = desc.Unmarshal([]byte(peerC.pc.LocalDescription().SDP))
			assert.NoError(t, err)

			trackASeen := false
			trackBSeen := false
			for _, md := range desc.MediaDescriptions {
				for _, attr := range md.Attributes {
					switch attr.Key {
					case sdp.AttrKeySSRC:
						split := strings.Split(attr.Value, " ")
						ssrc, err := strconv.ParseUint(split[0], 10, 32)
						assert.NoError(t, err)
						if uint32(ssrc) == trackA.SSRC() {
							trackASeen = true
						} else if uint32(ssrc) == trackB.SSRC() {
							trackBSeen = true
						}
						log.Infof("%v", ssrc)
					}
				}
			}

			assert.True(t, trackASeen)
			assert.True(t, trackBSeen)

			close(done)
		})

		sendRTPUntilDone(done, t, []*webrtc.Track{trackB})
	})

	sendRTPUntilDone(done, t, []*webrtc.Track{trackA})
}
