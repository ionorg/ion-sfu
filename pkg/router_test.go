package sfu

import (
	"context"
	"math/rand"
	"testing"

	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

func TestRouter(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	pubsfu, pub, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	track, err := pub.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = pub.AddTrack(track)
	assert.NoError(t, err)

	done := make(chan bool)
	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())
	pubsfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver, _ []*webrtc.Stream) {
		receiver := NewWebRTCVideoReceiver(WebRTCVideoReceiverConfig{}, track)
		router := NewRouter("id", receiver)
		assert.Equal(t, router.receiver, receiver)

		subsfu, sub, err := newPair(webrtc.Configuration{}, api)
		assert.NoError(t, err)

		ontrackFired := make(chan bool)
		sub.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver, _ []*webrtc.Stream) {
			out, err := track.ReadRTP()
			assert.NoError(t, err)

			assert.Equal(t, []byte{0x10, 0x01, 0x02, 0x03, 0x04}, out.Payload)
			onReadRTPFiredFunc()
			close(ontrackFired)
		})

		subtrack, err := subsfu.NewTrack(webrtc.DefaultPayloadTypeVP8, track.SSRC(), "video", "pion")
		assert.NoError(t, err)

		s, err := subsfu.AddTrack(subtrack)
		assert.NoError(t, err)

		err = signalPair(subsfu, sub)
		assert.NoError(t, err)

		subPid := "subpid"
		sender := NewWebRTCSender(subtrack, s)
		router.AddSender(subPid, sender)
		assert.Len(t, router.senders, 1)
		assert.Equal(t, sender, router.senders[subPid])

		<-ontrackFired

		// test deleting sub
		router.DelSub(subPid)
		assert.Len(t, router.senders, 0)

		// add sub back to test close
		router.AddSender(subPid, sender)
		// assert.Contains(t, router.stats(), "router:")
		router.Close()
		assert.Len(t, router.senders, 0)
		assert.True(t, sender.stop)
		assert.True(t, receiver.stop)
		close(done)
	})

	err = signalPair(pub, pubsfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{track})
	<-done
}

func TestRouterPartialReadCanClose(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	pubsfu, pub, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	track, err := pub.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = pub.AddTrack(track)
	assert.NoError(t, err)

	subClosed := make(chan bool)
	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())
	pubsfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver, _ []*webrtc.Stream) {
		receiver := NewWebRTCVideoReceiver(WebRTCVideoReceiverConfig{}, track)
		router := NewRouter("id", receiver)
		subsfu, sub, err := newPair(webrtc.Configuration{}, api)
		assert.NoError(t, err)

		sub.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver, _ []*webrtc.Stream) {
			onReadRTPFiredFunc()
		})

		subtrack, err := subsfu.NewTrack(webrtc.DefaultPayloadTypeVP8, track.SSRC(), "video", "pion")
		assert.NoError(t, err)

		s, err := subsfu.AddTrack(subtrack)
		assert.NoError(t, err)

		err = signalPair(subsfu, sub)
		assert.NoError(t, err)

		subPid := "subpid"
		sender := NewWebRTCSender(subtrack, s)
		router.AddSender(subPid, sender)

		<-onReadRTPFired.Done()
		router.Close()
		assert.True(t, sender.stop)
		assert.True(t, receiver.stop)

		close(subClosed)
	})

	err = signalPair(pub, pubsfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{track})

	<-subClosed
}
