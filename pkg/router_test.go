package sfu

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/pion/rtcp"

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
	onTrackFired, onTrackFiredFunc := context.WithCancel(context.Background())
	pubsfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver) {
		receiver := NewWebRTCReceiver(ctx, track).(*WebRTCReceiver)
		router := NewRouter("id", receiver)
		assert.Equal(t, router.receivers, receiver)

		subsfu, sub, err := newPair(webrtc.Configuration{}, api)
		assert.NoError(t, err)

		ontrackFired := make(chan bool)
		sub.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
			out, err := track.ReadRTP()
			assert.NoError(t, err)

			assert.Equal(t, []byte{0x10, 0x01, 0x02, 0x03, 0x04}, out.Payload)
			onTrackFiredFunc()
			close(ontrackFired)
		})

		subtrack, err := subsfu.NewTrack(webrtc.DefaultPayloadTypeVP8, track.SSRC(), "video", "pion")
		assert.NoError(t, err)

		s, err := subsfu.AddTrack(subtrack)
		assert.NoError(t, err)

		err = signalPair(subsfu, sub)
		assert.NoError(t, err)

		subPid := "subpid"
		sender := NewWebRTCSender(ctx, subtrack, s).(*WebRTCSender)
		router.AddSender(subPid, sender)
		assert.Len(t, router.senders, 1)
		assert.Equal(t, sender, router.senders[subPid])
		assert.Contains(t, router.stats(), "track id:")

		<-ontrackFired

		sub.Close()
		receiver.Close()
		sender.Close()
		<-sender.ctx.Done()
		<-receiver.ctx.Done()
		close(done)

		subsfu.Close()
	})

	err = signalPair(pub, pubsfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onTrackFired.Done(), t, []*webrtc.Track{track})
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
	onTrackFired, onTrackFiredFunc := context.WithCancel(context.Background())
	pubsfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver) {
		receiver := NewWebRTCReceiver(ctx, track).(*WebRTCReceiver)
		router := NewRouter("id", receiver)
		subsfu, sub, err := newPair(webrtc.Configuration{}, api)
		assert.NoError(t, err)

		sub.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
			onTrackFiredFunc()
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

		<-onTrackFired.Done()
		receiver.Close()

		<-receiver.ctx.Done()

		close(subClosed)

		subsfu.Close()
		sub.Close()
	})

	err = signalPair(pub, pubsfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onTrackFired.Done(), t, []*webrtc.Track{track})

	<-subClosed

	pubsfu.Close()
	pub.Close()
}

func TestSendersNackRateLimit(t *testing.T) {
	report := test.CheckRoutines(t)
	defer report()

	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	pubsfu, pub, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	onTimeout, onTimeoutFunc := context.WithCancel(context.Background())
	track, err := pub.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = pub.AddTrack(track)
	assert.NoError(t, err)

	ctx := context.Background()
	done := make(chan bool)
	pubsfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver) {
		receiver := NewWebRTCReceiver(ctx, track).(*WebRTCReceiver)
		router := NewRouter("id", receiver)
		assert.Equal(t, router.receivers, receiver)

		subsfu, sub, err := newPair(webrtc.Configuration{}, api)
		assert.NoError(t, err)

		ontrackFired := make(chan bool)
		sub.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
			out, err := track.ReadRTP()
			assert.NoError(t, err)

			assert.Equal(t, []byte{0x10, 0x01, 0x02, 0x03, 0x04}, out.Payload)
			close(ontrackFired)
		})

		subtrack, err := subsfu.NewTrack(webrtc.DefaultPayloadTypeVP8, track.SSRC(), "video", "pion")
		assert.NoError(t, err)

		s, err := subsfu.AddTrack(subtrack)
		assert.NoError(t, err)

		err = signalPair(subsfu, sub)
		assert.NoError(t, err)

		subPid := "subpid"
		sender := NewWebRTCSender(ctx, subtrack, s).(*WebRTCSender)
		router.AddSender(subPid, sender)
		assert.Len(t, router.senders, 1)
		assert.Equal(t, sender, router.senders[subPid])
		assert.Contains(t, router.stats(), "track id:")
		<-ontrackFired
		routerConfig.MaxNackTime = 1
		timer := time.After(1100 * time.Millisecond)
		nackCounter := 0
		router.lastNack = time.Now().Unix()
	nckloop:
		for {
			select {
			case <-timer:
				onTimeoutFunc()
				break nckloop
			case pkt := <-receiver.rtcpCh:
				if nck, ok := pkt.(*rtcp.TransportLayerNack); ok && nck.MediaSSRC == 456 {
					nackCounter++
				}
			default:
				nack := &rtcp.TransportLayerNack{
					SenderSSRC: 123,
					MediaSSRC:  456,
					Nacks:      []rtcp.NackPair{{PacketID: 789}},
				}
				sender.rtcpCh <- nack
				time.Sleep(25 * time.Millisecond)
			}
		}
		assert.LessOrEqual(t, nackCounter, 2)
		sub.Close()
		receiver.Close()
		sender.Close()
		<-receiver.ctx.Done()
		close(done)

		subsfu.Close()
	})

	err = signalPair(pub, pubsfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onTimeout.Done(), t, []*webrtc.Track{track})
	<-done

	pubsfu.Close()
	pub.Close()
}
