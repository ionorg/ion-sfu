package transport

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/pion/ion-sfu/pkg/media"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v2"
	webrtcmedia "github.com/pion/webrtc/v2/pkg/media"
	"github.com/stretchr/testify/assert"
)

func TestTypeReturnsWebRTCTransportType(t *testing.T) {
	me := media.Engine{}
	me.MediaEngine.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))
	pc, _ := api.NewPeerConnection(webrtc.Configuration{})

	pub := NewWebRTCTransport("pub", pc, &me)

	assert.Equal(t, pub.Type(), TypeWebRTCTransport)
}

func TestMediaEngineReturnsMediaEngine(t *testing.T) {
	me := &media.Engine{}
	me.MediaEngine.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))
	pc, _ := api.NewPeerConnection(webrtc.Configuration{})

	pub := NewWebRTCTransport("pub", pc, me)

	assert.Equal(t, pub.MediaEngine(), me)
}

func TestWebRTCTransportCloseHandlerOnlyOnce(t *testing.T) {
	me := media.Engine{}
	me.MediaEngine.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))
	pc, _ := api.NewPeerConnection(webrtc.Configuration{})

	pub := NewWebRTCTransport("pub", pc, &me)

	count := 0
	pub.OnClose(func() {
		count++
	})

	pub.Close()
	pub.Close()

	if count != 1 {
		t.Fatal("OnClose called on already closed transport")
	}
}

func sendVideoUntilDone(done <-chan struct{}, t *testing.T, tracks []*webrtc.Track) {
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			for _, track := range tracks {
				assert.NoError(t, track.WriteSample(webrtcmedia.Sample{Data: []byte{0x01, 0x02, 0x03, 0x04}, Samples: 1}))
			}
		case <-done:
			return
		}
	}
}

func TestRecv(t *testing.T) {
	me := &media.Engine{}
	me.MediaEngine.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))

	remote, _ := api.NewPeerConnection(webrtc.Configuration{})
	pub, _ := api.NewPeerConnection(webrtc.Configuration{})
	recv := NewWebRTCTransport("mid", pub, me)

	track, _ := remote.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	_, err := remote.AddTrack(track)
	assert.NoError(t, err)

	pub.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		recv.AddInTrack(track)
	})

	// Negotiate
	offer, _ := remote.CreateOffer(nil)
	_ = remote.SetLocalDescription(offer)
	_ = pub.SetRemoteDescription(offer)
	answer, _ := pub.CreateAnswer(nil)
	_ = pub.SetLocalDescription(answer)
	_ = remote.SetRemoteDescription(answer)

	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())

	go func() {
		for {
			out, _ := recv.ReadRTP()
			assert.Equal(t, []byte{0x10, 0x01, 0x02, 0x03, 0x04}, out.Payload)
			onReadRTPFiredFunc()
		}
	}()

	sendVideoUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{track})

	assert.Equal(t, 1, len(recv.GetInTracks()))
	assert.NoError(t, pub.Close())
	assert.NoError(t, remote.Close())
}

// Should return error when payload type mapping has not been initialized.
// Here we support VP8 on sub and VP9 on pub, resulting in mismatch.
func TestSendReturnsPtNotSupportedWithWhenPtDoesntExistInMapping(t *testing.T) {
	fe := &media.Engine{}
	fe.MediaEngine.RegisterCodec(webrtc.NewRTPVP8Codec(webrtc.DefaultPayloadTypeVP8, 90000))
	te := &media.Engine{}
	te.MediaEngine.RegisterCodec(webrtc.NewRTPVP9Codec(webrtc.DefaultPayloadTypeVP9, 90000))
	api := webrtc.NewAPI(webrtc.WithMediaEngine(te.MediaEngine))

	// Initialize mapping
	te.MapFromEngine(fe)

	sub, _ := api.NewPeerConnection(webrtc.Configuration{})
	send := NewWebRTCTransport("sender", sub, te)

	track, _ := webrtc.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion", webrtc.NewRTPVP8Codec(webrtc.DefaultPayloadTypeVP8, 90000))

	_, err := send.AddOutTrack("mid", track)

	assert.Equal(t, errPtNotSupported, err)
}

// Should return error when payload type mapping has not been initialized.
func TestSendReturnsPtNotSupportedWithMappingNotInitialized(t *testing.T) {
	me := &media.Engine{}
	me.MediaEngine.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))

	sub, _ := api.NewPeerConnection(webrtc.Configuration{})
	send := NewWebRTCTransport("sender", sub, me)

	track, _ := webrtc.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion", webrtc.NewRTPVP8Codec(webrtc.DefaultPayloadTypeVP8, 90000))

	_, err := send.AddOutTrack("mid", track)

	assert.Equal(t, errPtNotSupported, err)
}

func TestSend(t *testing.T) {
	rawPkt := []byte{
		0x90, 0xe0, 0x69, 0x8f, 0xd9, 0xc2, 0x93, 0xda, 0x1c, 0x64,
		0x27, 0x82, 0x00, 0x01, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x98, 0x36, 0xbe, 0x88, 0x9e,
	}

	rtp := &rtp.Packet{}
	_ = rtp.Unmarshal(rawPkt)

	me := &media.Engine{}
	me.MediaEngine.RegisterCodec(webrtc.NewRTPVP8Codec(webrtc.DefaultPayloadTypeVP8, 90000))
	subAPI := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))
	remoteAPI := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))

	// Initialize mapping
	me.MapFromEngine(me)

	remote, _ := remoteAPI.NewPeerConnection(webrtc.Configuration{})
	sub, _ := subAPI.NewPeerConnection(webrtc.Configuration{})

	_, onReadRTPFiredFunc := context.WithCancel(context.Background())

	_, _ = remote.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})

	remote.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		for {
			packet, err := track.ReadRTP()
			assert.NoError(t, err)
			assert.Equal(t, rtp, packet)
			onReadRTPFiredFunc()
		}
	})

	send := NewWebRTCTransport("sender", sub, me)

	track, _ := webrtc.NewTrack(webrtc.DefaultPayloadTypeVP8, rtp.SSRC, "video", "pion", webrtc.NewRTPVP8Codec(webrtc.DefaultPayloadTypeVP8, 90000))
	_, _ = send.AddOutTrack("video", track)

	// Negotiate
	offer, _ := remote.CreateOffer(nil)
	_ = remote.SetLocalDescription(offer)
	_ = sub.SetRemoteDescription(offer)
	answer, _ := sub.CreateAnswer(nil)
	_ = sub.SetLocalDescription(answer)
	_ = remote.SetRemoteDescription(answer)

	err := send.WriteRTP(rtp)
	assert.NoError(t, err)

	assert.NoError(t, sub.Close())
	assert.NoError(t, remote.Close())
}

// Tests that VP8 codec payloadtype is correctly mapped from 96->99
func TestSendWithCodecMapping(t *testing.T) {
	rawPkt := []byte{
		0x90, 0xe0, 0x69, 0x8f, 0xd9, 0xc2, 0x93, 0xda, 0x1c, 0x64,
		0x27, 0x82, 0x00, 0x01, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x98, 0x36, 0xbe, 0x88, 0x9e,
	}

	pkt := &rtp.Packet{}
	_ = pkt.Unmarshal(rawPkt)

	me := &media.Engine{}
	customPayloadTypeVP8 := uint8(99)
	me.MediaEngine.RegisterCodec(webrtc.NewRTPVP8Codec(customPayloadTypeVP8, 90000))
	subAPI := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))
	remoteAPI := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))

	// Initialize mapping
	te := &media.Engine{}
	te.MediaEngine.RegisterCodec(webrtc.NewRTPVP8Codec(webrtc.DefaultPayloadTypeVP8, 90000))
	me.MapFromEngine(te)

	remote, _ := remoteAPI.NewPeerConnection(webrtc.Configuration{})
	sub, _ := subAPI.NewPeerConnection(webrtc.Configuration{})

	_, onReadRTPFiredFunc := context.WithCancel(context.Background())

	_, _ = remote.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})

	expectedPkt := &rtp.Packet{}
	_ = expectedPkt.Unmarshal(rawPkt)
	expectedPkt.PayloadType = customPayloadTypeVP8
	remote.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		for {
			packet, err := track.ReadRTP()
			assert.NoError(t, err)
			assert.Equal(t, expectedPkt, packet)
			onReadRTPFiredFunc()
		}
	})

	send := NewWebRTCTransport("sender", sub, me)

	track, _ := webrtc.NewTrack(webrtc.DefaultPayloadTypeVP8, pkt.SSRC, "video", "pion", webrtc.NewRTPVP8Codec(webrtc.DefaultPayloadTypeVP8, 90000))
	_, err := send.AddOutTrack("video", track)
	assert.NoError(t, err)

	// Negotiate
	offer, _ := remote.CreateOffer(nil)
	_ = remote.SetLocalDescription(offer)
	_ = sub.SetRemoteDescription(offer)
	answer, _ := sub.CreateAnswer(nil)
	_ = sub.SetLocalDescription(answer)
	_ = remote.SetRemoteDescription(answer)

	err = send.WriteRTP(pkt)
	assert.NoError(t, err)

	assert.NoError(t, sub.Close())
	assert.NoError(t, remote.Close())
}

func TestSendAudio(t *testing.T) {
	rawPkt := []byte{
		0x90, 0xe0, 0x69, 0x8f, 0xd9, 0xc2, 0x93, 0xda, 0x1c, 0x64,
		0x27, 0x82, 0x00, 0x01, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x98, 0x36, 0xbe, 0x88, 0x9e,
	}

	rtp := &rtp.Packet{}
	rtp.PayloadType = webrtc.DefaultPayloadTypeOpus
	_ = rtp.Unmarshal(rawPkt)

	me := &media.Engine{}
	me.MediaEngine.RegisterCodec(webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, 4800))
	subAPI := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))
	remoteAPI := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine))

	// Initialize mapping
	me.MapFromEngine(me)

	remote, _ := remoteAPI.NewPeerConnection(webrtc.Configuration{})
	sub, _ := subAPI.NewPeerConnection(webrtc.Configuration{})

	_, onReadRTPFiredFunc := context.WithCancel(context.Background())

	_, _ = remote.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})

	remote.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		for {
			packet, err := track.ReadRTP()
			assert.NoError(t, err)
			assert.Equal(t, rtp, packet)
			onReadRTPFiredFunc()
		}
	})

	send := NewWebRTCTransport("sender", sub, me)

	track, _ := webrtc.NewTrack(webrtc.DefaultPayloadTypeOpus, rtp.SSRC, "audio", "pion", webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, 4800))
	_, _ = send.AddOutTrack("audio", track)

	// Negotiate
	offer, _ := remote.CreateOffer(nil)
	_ = remote.SetLocalDescription(offer)
	_ = sub.SetRemoteDescription(offer)
	answer, _ := sub.CreateAnswer(nil)
	_ = sub.SetLocalDescription(answer)
	_ = remote.SetRemoteDescription(answer)

	err := send.WriteRTP(rtp)
	assert.NoError(t, err)

	assert.NoError(t, sub.Close())
	assert.NoError(t, remote.Close())
}
