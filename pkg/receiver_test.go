package sfu

import (
	"context"
	"math/rand"
	"net"
	"testing"
	"time"

	"github.com/pion/ion-sfu/pkg/relay"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/stretchr/testify/assert"
)

func sendRTPUntilDone(done <-chan struct{}, t *testing.T, tracks []*webrtc.Track) {
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			for _, track := range tracks {
				assert.NoError(t, track.WriteSample(media.Sample{Data: []byte{0x01, 0x02, 0x03, 0x04}, Samples: 1}))
			}
		case <-done:
			return
		}
	}
}

func TestWebRTCAudioReceiverRTPForwarding(t *testing.T) {
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

		out, err = receiver.ReadRTP()
		assert.Nil(t, out)
		assert.Equal(t, errReceiverClosed, err)

		onReadRTPFiredFunc()
	})

	err = signalPair(remote, sfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{track})
}

func TestWebRTCVideoReceiverRTPForwarding(t *testing.T) {
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
		receiver := NewWebRTCVideoReceiver(WebRTCVideoReceiverConfig{}, track)
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
	})

	err = signalPair(remote, sfu)
	assert.NoError(t, err)

	sendRTPUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{track})
}

func sendRTPToPipeUntilDone(done <-chan struct{}, t *testing.T, track *webrtc.Track, conn net.Conn) {
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			pkt := track.Packetizer().Packetize([]byte{0x01, 0x02, 0x03, 0x04}, 1)[0]
			buf, err := pkt.Marshal()
			assert.NoError(t, err)
			_, err = conn.Write(buf)
			assert.NoError(t, err)
		case <-done:
			return
		}
	}
}

func TestRelayReceiverRTPForwarding(t *testing.T) {
	aPipe, bPipe := net.Pipe()
	session, err := relay.NewSessionRTP(aPipe)
	assert.NoError(t, err)

	ssrc := uint32(5000)
	readStream, err := session.OpenReadStream(ssrc)
	assert.NoError(t, err)

	track, err := webrtc.NewTrack(webrtc.DefaultPayloadTypeOpus, ssrc, "audio", "pion", webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, 48000))
	assert.NoError(t, err)

	receiver := NewRelayReceiver(track, readStream)

	onReadFired, onReadFiredFunc := context.WithCancel(context.Background())
	go func() {
		_, err := receiver.ReadRTP()
		assert.NoError(t, err)
		onReadFiredFunc()
	}()

	sendRTPToPipeUntilDone(onReadFired.Done(), t, track, bPipe)

	assert.Equal(t, track, receiver.Track())
	assert.Nil(t, receiver.GetPacket(0))
	assert.Contains(t, receiver.stats(), "payload")
	receiver.Close()
}
