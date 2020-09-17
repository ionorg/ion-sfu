package sfu

import (
	"context"
	"io"
	"math/rand"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/transport/test"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/stretchr/testify/assert"
)

func sendRTPUntilDone(done <-chan struct{}, t *testing.T, tracks []*webrtc.Track) {
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			for _, track := range tracks {
				err := track.WriteSample(media.Sample{Data: []byte{0x01, 0x02, 0x03, 0x04}, Samples: 1})
				if err == io.ErrClosedPipe {
					return
				}
				assert.NoError(t, err)
			}
		case <-done:
			return
		}
	}
}

func TestWebRTCAudioReceiverRTPForwarding(t *testing.T) {
	report := test.CheckRoutines(t)
	defer report()

	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	track, err := remote.NewTrack(webrtc.DefaultPayloadTypeOpus, rand.Uint32(), "audio", "pion")
	assert.NoError(t, err)
	_, err = remote.AddTrack(track)
	assert.NoError(t, err)

	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())
	sfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver) {
		receiver := NewWebRTCReceiver(context.Background(), track)
		assert.Equal(t, track, receiver.Track())

		rtcpCh := receiver.ReadRTCP()
		assert.Nil(t, rtcpCh)
		err = receiver.WriteRTCP(nil)
		assert.Equal(t, io.ErrClosedPipe, err)
		rtp := receiver.GetPacket(0)
		assert.Nil(t, rtp)

		out := receiver.ReadRTP()
		go func() {
			timeout := time.NewTimer(1 * time.Second)
			for {
				select {
				case <-timeout.C:
					t.Fatal("Receiver dont close")
				case pkt, opn := <-out:
					if !opn {
						return
					}
					assert.Equal(t, []byte{0x01, 0x02, 0x03, 0x04}, pkt.Payload)
				}
			}
		}()
		receiver.Close()
		onReadRTPFiredFunc()
	})

	err = signalPair(remote, sfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{track})

	assert.NoError(t, remote.Close())
	assert.NoError(t, sfu.Close())
}

func TestWebRTCVideoReceiverRTPForwarding(t *testing.T) {
	report := test.CheckRoutines(t)
	defer report()

	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	track, err := remote.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remote.AddTrack(track)
	assert.NoError(t, err)

	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())
	sfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver) {
		ctx := context.Background()
		receiver := NewWebRTCReceiver(ctx, track)
		assert.Equal(t, track, receiver.Track())

		out := receiver.ReadRTP()
		pkt := <-out
		assert.Equal(t, []byte{0x10, 0x01, 0x02, 0x03, 0x04}, pkt.Payload)

		// Test fetching from buffer
		buf := receiver.GetPacket(pkt.SequenceNumber)
		assert.Equal(t, pkt, buf)

		// GetPacket on unbuffered returns nil packet
		buf = receiver.GetPacket(1)
		assert.Nil(t, buf)

		onReadRTPFiredFunc()

		receiver.Close()
	})

	err = signalPair(remote, sfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{track})

	assert.NoError(t, sfu.Close())
	assert.NoError(t, remote.Close())
}

func TestWebRTCVideoReceiver_rembLoop(t *testing.T) {
	report := test.CheckRoutines(t)
	defer report()

	routerConfig.Video.REMBCycle = 1
	codec := webrtc.NewRTPCodec(webrtc.RTPCodecTypeVideo, "video", 90000, 0, "", 5, &codecs.VP8Payloader{})
	codec.RTCPFeedback = []webrtc.RTCPFeedback{{Type: webrtc.TypeRTCPFBGoogREMB}}

	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	me.RegisterCodec(codec)
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))

	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	track, err := remote.NewTrack(5, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)
	_, err = remote.AddTrack(track)
	assert.NoError(t, err)

	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())
	sfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver) {
		assert.NoError(t, err)

		videoReceiver := NewWebRTCReceiver(context.Background(), track)
		assert.NotNil(t, videoReceiver)

		rtcpCh := videoReceiver.ReadRTCP()
		rtcpPacket := <-rtcpCh

		assert.NotNil(t, rtcpPacket)
		err = rtcpPacket.Unmarshal([]byte{1})
		assert.Error(t, err)

		// it could either be packet too short or buffer too short
		assert.Contains(t, err.Error(), "too short")

		bytes, err := rtcpPacket.Marshal()
		assert.NoError(t, err)
		assert.NotNil(t, bytes)

		// ensure that the rtcp from the channel is a packet
		_, ok := rtcpPacket.(rtcp.Packet)
		assert.True(t, ok)

		onReadRTPFiredFunc()

		videoReceiver.Close()
	})

	err = signalPair(remote, sfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{track})

	assert.NoError(t, sfu.Close())
	assert.NoError(t, remote.Close())
}
