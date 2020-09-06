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
	sfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver, _ []*webrtc.Stream) {
		receiver := NewWebRTCAudioReceiver(track)
		assert.Equal(t, track, receiver.Track())

		rtcp, err := receiver.ReadRTCP()
		assert.Nil(t, rtcp)
		assert.Equal(t, errMethodNotSupported, err)
		err = receiver.WriteRTCP(nil)
		assert.Equal(t, errMethodNotSupported, err)
		rtp := receiver.GetPacket(0)
		assert.Nil(t, rtp)

		out, err := receiver.ReadRTP()
		assert.NoError(t, err)
		assert.Equal(t, []byte{0x01, 0x02, 0x03, 0x04}, out.Payload)

		receiver.Close()

		onReadRTPFiredFunc()

		out, err = receiver.ReadRTP()
		assert.Nil(t, out)
		assert.Equal(t, io.EOF, err)
	})

	err = signalPair(remote, sfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{track})

	assert.NoError(t, sfu.Close())
	assert.NoError(t, remote.Close())
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
	sfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver, _ []*webrtc.Stream) {
		ctx := context.Background()
		receiver := NewWebRTCVideoReceiver(ctx, WebRTCVideoReceiverConfig{}, track)
		assert.Equal(t, track, receiver.Track())

		out, err := receiver.ReadRTP()
		assert.NoError(t, err)
		assert.Equal(t, []byte{0x10, 0x01, 0x02, 0x03, 0x04}, out.Payload)

		// Test fetching from buffer
		buf := receiver.GetPacket(out.SequenceNumber)
		assert.Equal(t, out, buf)

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
	sfu.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver, _ []*webrtc.Stream) {
		assert.NoError(t, err)
		videoConfig := WebRTCVideoReceiverConfig{
			REMBCycle: 1,
		}

		videoReceiver := NewWebRTCVideoReceiver(context.Background(), videoConfig, track)
		assert.NotNil(t, videoReceiver)

		rtcpPacket := <-videoReceiver.rtcpCh

		assert.NotNil(t, rtcpPacket)
		err = rtcpPacket.Unmarshal([]byte{1})
		assert.Error(t, err)

		//it could either be packet too short or buffer too short
		assert.Contains(t, err.Error(), "too short")

		bytes, err := rtcpPacket.Marshal()
		assert.NoError(t, err)
		assert.NotNil(t, bytes)

		//ensure that the rtcp from the channel is a packet
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
