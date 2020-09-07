package sfu

import (
	"context"
	"math/rand"
	"testing"

	"github.com/pion/transport/test"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

func TestRouter(t *testing.T) {
	report := test.CheckRoutines(t)
	defer report()

	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	pubsfu, pub, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	track, err := pub.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = pub.AddTrack(track)
	assert.NoError(t, err)

	ctx := context.Background()
	done := make(chan bool)
	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())
	pubsfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver, _ []*webrtc.Stream) {
		receiver := NewWebRTCVideoReceiver(ctx, WebRTCVideoReceiverConfig{}, track)
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
		sender := NewWebRTCSender(ctx, subtrack, s)
		router.AddSender(subPid, sender)
		assert.Len(t, router.senders, 1)
		assert.Equal(t, sender, router.senders[subPid])
		assert.Contains(t, router.stats(), "track id:")

		<-ontrackFired

		sub.Close()
		router.Close()
		router.mu.RLock()
		assert.Len(t, router.senders, 0)
		router.mu.RUnlock()
		<-sender.ctx.Done()
		<-receiver.ctx.Done()
		close(done)

		subsfu.Close()
	})

	err = signalPair(pub, pubsfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{track})
	<-done

	pubsfu.Close()
	pub.Close()
}

func TestRouterPartialReadCanClose(t *testing.T) {
	report := test.CheckRoutines(t)
	defer report()

	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	pubsfu, pub, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	track, err := pub.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = pub.AddTrack(track)
	assert.NoError(t, err)

	ctx := context.Background()
	subClosed := make(chan bool)
	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())
	pubsfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver, _ []*webrtc.Stream) {
		receiver := NewWebRTCVideoReceiver(ctx, WebRTCVideoReceiverConfig{}, track)
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
		sender := NewWebRTCSender(ctx, subtrack, s)
		router.AddSender(subPid, sender)

		<-onReadRTPFired.Done()
		router.Close()

		<-sender.ctx.Done()
		<-receiver.ctx.Done()

		close(subClosed)

		subsfu.Close()
		sub.Close()
	})

	err = signalPair(pub, pubsfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{track})

	<-subClosed

	pubsfu.Close()
	pub.Close()
}
