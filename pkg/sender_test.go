package sfu

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

var rawPkt = []byte{
	0x90, 0xe0, 0x69, 0x8f, 0xd9, 0xc2, 0x93, 0xda, 0x1c, 0x64,
	0x27, 0x82, 0x00, 0x01, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x98, 0x36, 0xbe, 0x88, 0x9e,
}

func signalPair(pcOffer *webrtc.PeerConnection, pcAnswer *webrtc.PeerConnection) error {
	offer, err := pcOffer.CreateOffer(nil)
	if err != nil {
		return err
	}
	gatherComplete := webrtc.GatheringCompletePromise(pcOffer)
	if err = pcOffer.SetLocalDescription(offer); err != nil {
		return err
	}
	<-gatherComplete
	if err = pcAnswer.SetRemoteDescription(*pcOffer.LocalDescription()); err != nil {
		return err
	}

	answer, err := pcAnswer.CreateAnswer(nil)
	if err != nil {
		return err
	}
	if err = pcAnswer.SetLocalDescription(answer); err != nil {
		return err
	}
	return pcOffer.SetRemoteDescription(*pcAnswer.LocalDescription())
}

func sendRTPWithSenderUntilDone(done <-chan struct{}, t *testing.T, track *webrtc.Track, sender Sender) {
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			pkt := track.Packetizer().Packetize([]byte{0x01, 0x02, 0x03, 0x04}, 1)[0]
			sender.WriteRTP(pkt)
		case <-done:
			return
		}
	}
}

func TestSenderRTPForwarding(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	rtp := &rtp.Packet{}
	err = rtp.Unmarshal(rawPkt)
	assert.NoError(t, err)

	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())
	remote.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver, _ []*webrtc.Stream) {
		out, err := track.ReadRTP()
		assert.NoError(t, err)

		assert.Equal(t, []byte{0x10, 0x01, 0x02, 0x03, 0x04}, out.Payload)
		onReadRTPFiredFunc()
	})

	track, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)

	s, err := sfu.AddTrack(track)
	assert.NoError(t, err)

	sender := NewWebRTCSender(track, s)
	assert.NotNil(t, sender)

	err = signalPair(sfu, remote)
	assert.NoError(t, err)

	sendRTPWithSenderUntilDone(onReadRTPFired.Done(), t, track, sender)

	assert.Contains(t, sender.stats(), "payload")

	sender.Close()
	sender.Close()

	_, err = sender.ReadRTCP()
	assert.Error(t, err)
}

func sendRTCPUntilDone(done <-chan struct{}, t *testing.T, pc *webrtc.PeerConnection, pkt rtcp.Packet) {
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			assert.NoError(t, pc.WriteRTCP([]rtcp.Packet{pkt}))
		case <-done:
			return
		}
	}
}

func TestSenderRTCPForwarding(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	rtp := &rtp.Packet{}
	err = rtp.Unmarshal(rawPkt)
	assert.NoError(t, err)

	track, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)

	s, err := sfu.AddTrack(track)
	assert.NoError(t, err)

	sender := NewWebRTCSender(track, s)
	assert.NotNil(t, sender)

	err = signalPair(sfu, remote)
	assert.NoError(t, err)

	pkt := &rtcp.PictureLossIndication{
		SenderSSRC: track.SSRC(),
		MediaSSRC:  track.SSRC(),
	}

	onReadRTCPFired, onReadRTCPFiredFunc := context.WithCancel(context.Background())
	go func() {
		for {
			rtcp, err := sender.ReadRTCP()
			assert.NoError(t, err)

			assert.Equal(t, pkt, rtcp)
			onReadRTCPFiredFunc()
		}
	}()

	// pkt := &rtcp.ReceiverEstimatedMaximumBitrate{
	// 	SenderSSRC: track.SSRC(),
	// 	Bitrate:    1000,
	// 	SSRCs:      []uint32{track.SSRC()},
	// }

	sendRTCPUntilDone(onReadRTCPFired.Done(), t, remote, pkt)
}

func TestSenderRTCPREMBForwarding(t *testing.T) {
	rtcpfb = []webrtc.RTCPFeedback{
		{Type: webrtc.TypeRTCPFBGoogREMB},
	}

	me := webrtc.MediaEngine{}
	codec := webrtc.NewRTPVP8CodecExt(webrtc.DefaultPayloadTypeVP8, 9000, rtcpfb, "")
	me.RegisterCodec(codec)
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	rtp := &rtp.Packet{}
	err = rtp.Unmarshal(rawPkt)
	assert.NoError(t, err)

	track, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)

	s, err := sfu.AddTrack(track)
	assert.NoError(t, err)

	sender := NewWebRTCSender(track, s)
	assert.NotNil(t, sender)

	err = signalPair(sfu, remote)
	assert.NoError(t, err)

	expected := &rtcp.ReceiverEstimatedMaximumBitrate{
		SenderSSRC: 1,
		Bitrate:    100000,
		SSRCs:      []uint32{track.SSRC()},
	}

	onReadRTCPFired, onReadRTCPFiredFunc := context.WithCancel(context.Background())
	go func() {
		for {
			rtcp, err := sender.ReadRTCP()
			assert.NoError(t, err)

			assert.Equal(t, expected, rtcp)
			onReadRTCPFiredFunc()
		}
	}()

	pkt := &rtcp.ReceiverEstimatedMaximumBitrate{
		SenderSSRC: 1,
		Bitrate:    1000,
		SSRCs:      []uint32{track.SSRC()},
	}

	sendRTCPUntilDone(onReadRTCPFired.Done(), t, remote, pkt)
}
