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

func createPeer(t *testing.T, session *Session, api *webrtc.API) (*WebRTCTransport, *webrtc.PeerConnection, *webrtc.Track, error) {
	remote, err := api.NewPeerConnection(conf.configuration)
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
	peer, err := signalPeer(session, remote)
	if err != nil {
		return nil, nil, nil, err
	}

	remoteCloseFired, remoteCloseFiredFunc := context.WithCancel(context.Background())
	remote.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		if connectionState == webrtc.ICEConnectionStateClosed {
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

	session := NewSession("session")

	engine := MediaEngine{}
	err = engine.PopulateFromSDP(*remote.LocalDescription())
	assert.NoError(t, err)

	peer, err := NewWebRTCTransport(session, engine, conf)
	assert.NoError(t, err)

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

	session := NewSession("session")

	// Setup remote <-> peer for a
	peerA, err := signalPeer(session, remoteA)
	assert.NoError(t, err)

	// Add a pub track for remote B
	trackB, err := remoteB.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remoteB.AddTrack(trackB)
	assert.NoError(t, err)

	// Setup remote <-> peer for b
	peerB, err := signalPeer(session, remoteB)
	assert.NoError(t, err)

	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())
	session.OnClose(func() {
		onReadRTPFiredFunc()
	})

	onNegotationNeededFired, onNegotationNeededFiredFunc := context.WithCancel(context.Background())
	peerA.OnNegotiationNeeded(func() {
		onNegotationNeededFiredFunc()
	})

	session.AddTransport(peerA)

	trackADone := waitForRouter(peerA, trackA.SSRC())
	sendRTPUntilDone(trackADone, t, []*webrtc.Track{trackA})

	assert.Len(t, peerA.Routers(), 1)

	session.AddTransport(peerB)

	trackBDone := waitForRouter(peerB, trackB.SSRC())
	sendRTPUntilDone(trackBDone, t, []*webrtc.Track{trackB})

	assert.Len(t, peerB.Routers(), 1)

	assert.Len(t, peerA.GetRouter(trackA.SSRC()).senders, 1)
	assert.Len(t, peerB.GetRouter(trackB.SSRC()).senders, 1)

	peerA.Close()
	assert.Len(t, peerB.GetRouter(trackB.SSRC()).senders, 0)

	peerB.Close()

	<-onNegotationNeededFired.Done()
	<-onReadRTPFired.Done()
}

func Test3PeerConcurrrentJoin(t *testing.T) {
	session := NewSession("session")
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	peerA, remoteA, trackA, err := createPeer(t, session, api)
	assert.NoError(t, err)

	peerB, remoteB, trackB, err := createPeer(t, session, api)
	assert.NoError(t, err)

	peerC, remoteC, trackC, err := createPeer(t, session, api)
	assert.NoError(t, err)

	peerAGotTracks := make(chan bool)
	peerA.OnNegotiationNeeded(func() {
		offer, err := peerA.CreateOffer()
		assert.NoError(t, err)

		desc := sdp.SessionDescription{}
		err = desc.Unmarshal([]byte(offer.SDP))
		assert.NoError(t, err)

		trackBSeen := false
		trackCSeen := false
		for _, md := range desc.MediaDescriptions {
			for _, attr := range md.Attributes {
				if attr.Key == sdp.AttrKeySSRC {
					split := strings.Split(attr.Value, " ")
					ssrc, err := strconv.ParseUint(split[0], 10, 32)
					assert.NoError(t, err)
					if !trackBSeen && uint32(ssrc) == trackB.SSRC() {
						trackBSeen = true
					} else if !trackCSeen && uint32(ssrc) == trackC.SSRC() {
						trackCSeen = true
					}
				}
			}
		}

		if trackBSeen && trackCSeen {
			close(peerAGotTracks)
		}

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
		err = peerA.SetRemoteDescription(answer)
		assert.NoError(t, err)
	})

	peerBGotTracks := make(chan bool)
	peerB.OnNegotiationNeeded(func() {
		offer, err := peerB.CreateOffer()
		assert.NoError(t, err)

		desc := sdp.SessionDescription{}
		err = desc.Unmarshal([]byte(offer.SDP))
		assert.NoError(t, err)

		trackASeen := false
		trackCSeen := false
		for _, md := range desc.MediaDescriptions {
			for _, attr := range md.Attributes {
				if attr.Key == sdp.AttrKeySSRC {
					split := strings.Split(attr.Value, " ")
					ssrc, err := strconv.ParseUint(split[0], 10, 32)
					assert.NoError(t, err)
					if !trackASeen && uint32(ssrc) == trackA.SSRC() {
						trackASeen = true
					} else if !trackCSeen && uint32(ssrc) == trackC.SSRC() {
						trackCSeen = true
					}
				}
			}
		}

		if trackASeen && trackCSeen {
			close(peerBGotTracks)
		}

		err = peerB.SetLocalDescription(offer)
		assert.NoError(t, err)
		gatherComplete := webrtc.GatheringCompletePromise(peerB.pc)

		<-gatherComplete

		err = remoteB.SetRemoteDescription(*peerB.pc.LocalDescription())
		assert.NoError(t, err)

		answer, err := remoteB.CreateAnswer(nil)
		assert.NoError(t, err)
		err = remoteB.SetLocalDescription(answer)
		assert.NoError(t, err)
		err = peerB.SetRemoteDescription(answer)
		assert.NoError(t, err)
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
				if attr.Key == sdp.AttrKeySSRC {
					split := strings.Split(attr.Value, " ")
					ssrc, err := strconv.ParseUint(split[0], 10, 32)
					assert.NoError(t, err)
					if !trackASeen && uint32(ssrc) == trackA.SSRC() {
						trackASeen = true
					} else if !trackBSeen && uint32(ssrc) == trackB.SSRC() {
						trackBSeen = true
					}
				}
			}
		}

		if trackASeen && trackBSeen {
			close(peerCGotTracks)
		}

		err = peerC.SetLocalDescription(offer)
		assert.NoError(t, err)
		gatherComplete := webrtc.GatheringCompletePromise(peerC.pc)

		<-gatherComplete

		err = remoteC.SetRemoteDescription(*peerC.pc.LocalDescription())
		assert.NoError(t, err)

		answer, err := remoteC.CreateAnswer(nil)
		assert.NoError(t, err)
		err = remoteC.SetLocalDescription(answer)
		assert.NoError(t, err)
		err = peerC.SetRemoteDescription(answer)
		assert.NoError(t, err)
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

	remoteA, err := api.NewPeerConnection(conf.configuration)
	assert.NoError(t, err)

	// Add a pub track for remote A
	trackA, err := remoteA.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remoteA.AddTrack(trackA)
	assert.NoError(t, err)

	session := NewSession("session")

	// Setup remote <-> peer for a
	peerA, err := signalPeer(session, remoteA)
	assert.NoError(t, err)

	session.AddTransport(peerA)

	trackADone := waitForRouter(peerA, trackA.SSRC())
	sendRTPUntilDone(trackADone, t, []*webrtc.Track{trackA})

	remoteB, err := api.NewPeerConnection(conf.configuration)
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
	engine := MediaEngine{}
	err = engine.PopulateFromSDP(offer)
	assert.NoError(t, err)
	gatherComplete := webrtc.GatheringCompletePromise(remoteB)
	peerB, err := NewWebRTCTransport(session, engine, conf)
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
			if attr.Key == sdp.AttrKeySSRC {
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

	trackBDone := waitForRouter(peerB, trackB.SSRC())
	sendRTPUntilDone(trackBDone, t, []*webrtc.Track{trackB})

	remoteC, err := api.NewPeerConnection(conf.configuration)
	assert.NoError(t, err)
	// Add transceiver to match number of recv tracks
	_, err = remoteC.AddTransceiverFromTrack(trackB)
	assert.NoError(t, err)

	// Add a pub track for remote B
	trackC, err := remoteC.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remoteC.AddTrack(trackC)
	assert.NoError(t, err)
	offer, err = remoteC.CreateOffer(nil)
	assert.NoError(t, err)
	err = remoteC.SetLocalDescription(offer)
	assert.NoError(t, err)
	gatherComplete = webrtc.GatheringCompletePromise(remoteC)
	engine = MediaEngine{}
	err = engine.PopulateFromSDP(offer)
	assert.NoError(t, err)
	peerC, err := NewWebRTCTransport(session, engine, conf)
	session.AddTransport(peerC)
	assert.NoError(t, err)
	<-gatherComplete
	err = peerC.SetRemoteDescription(*remoteC.LocalDescription())
	assert.NoError(t, err)
	log.Infof("create answer")
	answer, err = peerC.CreateAnswer()
	assert.NoError(t, err)
	err = peerC.SetLocalDescription(answer)
	assert.NoError(t, err)
	err = remoteC.SetRemoteDescription(*peerC.pc.LocalDescription())
	assert.NoError(t, err)

	desc = sdp.SessionDescription{}
	err = desc.Unmarshal([]byte(peerC.pc.LocalDescription().SDP))
	assert.NoError(t, err)

	trackASeen = false
	trackBSeen := false
	for _, md := range desc.MediaDescriptions {
		for _, attr := range md.Attributes {
			if attr.Key == sdp.AttrKeySSRC {
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
}

func TestPeerBWithAudioAndVideoWhenPeerAHasAudioOnly(t *testing.T) {
	// Create peer A with only audio
	meA := webrtc.MediaEngine{}
	meA.RegisterCodec(webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, 48000))
	apiA := webrtc.NewAPI(webrtc.WithMediaEngine(meA))

	remoteA, err := apiA.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)

	// Add a pub audio track for remote A
	trackAAudio, err := remoteA.NewTrack(webrtc.DefaultPayloadTypeOpus, rand.Uint32(), "audio", "pion")
	assert.NoError(t, err)
	_, err = remoteA.AddTrack(trackAAudio)
	assert.NoError(t, err)

	session := NewSession("session")

	// Setup remote <-> peer for a
	peerA, err := signalPeer(session, remoteA)
	assert.NoError(t, err)

	onTrackAAudio := waitForRouter(peerA, trackAAudio.SSRC())
	sendRTPUntilDone(onTrackAAudio, t, []*webrtc.Track{trackAAudio})

	// Create peer B with audio and video
	meB := webrtc.MediaEngine{}
	meB.RegisterCodec(webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, 48000))
	meB.RegisterCodec(webrtc.NewRTPVP8Codec(webrtc.DefaultPayloadTypeVP8, 90000))
	apiB := webrtc.NewAPI(webrtc.WithMediaEngine(meB))

	remoteB, err := apiB.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)

	// Add a pub audio track for remote B
	trackBAudio, err := remoteB.NewTrack(webrtc.DefaultPayloadTypeOpus, rand.Uint32(), "audio", "pion")
	assert.NoError(t, err)
	_, err = remoteB.AddTrack(trackBAudio)
	assert.NoError(t, err)

	// Add a pub video track for remote B
	trackBVideo, err := remoteB.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remoteB.AddTrack(trackBVideo)
	assert.NoError(t, err)

	peerB, err := signalPeer(session, remoteB)
	assert.NoError(t, err)

	onTrackBAudio := waitForRouter(peerB, trackBAudio.SSRC())
	sendRTPUntilDone(onTrackBAudio, t, []*webrtc.Track{trackBAudio})

	onTrackBVideo := waitForRouter(peerB, trackBVideo.SSRC())
	sendRTPUntilDone(onTrackBVideo, t, []*webrtc.Track{trackBVideo})

	// peer B should have peer A audio
	trackAAudioRouter := peerA.GetRouter(trackAAudio.SSRC())
	trackAAudioSenderForPeerB := trackAAudioRouter.senders[peerB.id]
	assert.NotNil(t, trackAAudioSenderForPeerB)

	// peer A should have peer B audio
	trackBAudioRouter := peerB.GetRouter(trackBAudio.SSRC())
	trackBAudioSenderForPeerA := trackBAudioRouter.senders[peerA.id]
	assert.NotNil(t, trackBAudioSenderForPeerA)

	// peer A should have peer B video
	trackAVideoRouter := peerB.GetRouter(trackBVideo.SSRC())
	trackAVideoSenderForPeerB := trackAVideoRouter.senders[peerA.id]
	assert.NotNil(t, trackAVideoSenderForPeerB)
}
