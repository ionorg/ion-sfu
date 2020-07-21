package sfu

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v2"
	"github.com/pion/webrtc/v2/pkg/media"
	"github.com/stretchr/testify/assert"
)

var rawPkt = []byte{
	0x90, 0xe0, 0x69, 0x8f, 0xd9, 0xc2, 0x93, 0xda, 0x1c, 0x64,
	0x27, 0x82, 0x00, 0x01, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x98, 0x36, 0xbe, 0x88, 0x9e,
}

// newPair creates two new peer connections (an offerer and an answerer) using
// the api.
func newPair(cfg webrtc.Configuration, api *webrtc.API) (pcOffer *webrtc.PeerConnection, pcAnswer *webrtc.PeerConnection, err error) {
	pca, err := api.NewPeerConnection(cfg)
	if err != nil {
		return nil, nil, err
	}

	pcb, err := api.NewPeerConnection(cfg)
	if err != nil {
		return nil, nil, err
	}

	return pca, pcb, nil
}

func signalPair(pcOffer *webrtc.PeerConnection, pcAnswer *webrtc.PeerConnection) error {
	offer, err := pcOffer.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err = pcOffer.SetLocalDescription(offer); err != nil {
		return err
	}
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

func sendVideoUntilDone(done <-chan struct{}, t *testing.T, tracks []*webrtc.Track) {
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

func TestSenderSendFromSFUArriveAtRemote(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	rtp := &rtp.Packet{}
	err = rtp.Unmarshal(rawPkt)
	assert.NoError(t, err)

	onReadRTPFired, onReadRTPFiredFunc := context.WithCancel(context.Background())
	remote.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		for {
			out, err := track.ReadRTP()
			assert.NoError(t, err)

			assert.Equal(t, []byte{0x10, 0x01, 0x02, 0x03, 0x04}, out.Payload)
			onReadRTPFiredFunc()
		}
	})

	track, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
	assert.NoError(t, err)

	trans, err := sfu.AddTransceiverFromTrack(track, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
		SendEncodings: []webrtc.RTPEncodingParameters{{
			RTPCodingParameters: webrtc.RTPCodingParameters{SSRC: track.SSRC(), PayloadType: webrtc.DefaultPayloadTypeVP8},
		}},
	})
	assert.NoError(t, err)

	sender := NewSender(track, trans)
	assert.NotNil(t, sender)

	err = signalPair(sfu, remote)
	assert.NoError(t, err)

	err = sender.WriteRTP(rtp)
	assert.NoError(t, err)

	sendVideoUntilDone(onReadRTPFired.Done(), t, []*webrtc.Track{track})
}
