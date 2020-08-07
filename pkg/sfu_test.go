package sfu

import (
	"net"
	"testing"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/relay"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

func TestSFU(t *testing.T) {
	s := NewSFU(Config{
		Log: log.Config{
			Level: "error",
		},
		WebRTC: WebRTCConfig{},
		Receiver: ReceiverConfig{
			Video: WebRTCVideoReceiverConfig{},
		},
	})

	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	remote, err := api.NewPeerConnection(cfg)
	assert.NoError(t, err)

	offer, err := remote.CreateOffer(nil)
	assert.NoError(t, err)
	err = remote.SetLocalDescription(offer)
	assert.NoError(t, err)

	transport, err := s.NewWebRTCTransport(1, offer)
	assert.NotNil(t, transport)
	assert.NoError(t, err)
}

func sendRelayUntilDone(done <-chan struct{}, t *testing.T, id uint32, conn net.Conn) {
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			pkt := &relay.Packet{
				Header: relay.Header{
					Version:   1,
					SessionID: id,
				},
				Payload: []byte{0x01, 0x02, 0x03, 0x04},
			}
			bin, err := pkt.Marshal()
			assert.NoError(t, err)
			_, err = conn.Write(bin)
			assert.NoError(t, err)
		case <-done:
			return
		}
	}
}

func TestSFUAcceptsRelayTransport(t *testing.T) {
	s := NewSFU(Config{
		Log: log.Config{
			Level: "error",
		},
		WebRTC: WebRTCConfig{},
		Receiver: ReceiverConfig{
			Video: WebRTCVideoReceiverConfig{},
		},
		Relay: RelayConfig{
			Port: 5557,
		},
	})

	sessionID := uint32(5)
	client := relay.NewClient(sessionID, "localhost:5557")
	assert.NotNil(t, client)

	done := make(chan struct{})
	go func() {
		for {
			session := s.getSession(sessionID)
			if session != nil && len(session.Transports()) == 1 {
				close(done)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	sendRelayUntilDone(done, t, sessionID, client)
	s.Close()
}

func TestSFUNewRelayTransport(t *testing.T) {
	s := NewSFU(Config{
		Log: log.Config{
			Level: "error",
		},
		WebRTC: WebRTCConfig{},
		Receiver: ReceiverConfig{
			Video: WebRTCVideoReceiverConfig{},
		},
		Relay: RelayConfig{
			Port: 5556,
		},
	})

	sessionID := uint32(5)
	transport, err := s.NewRelayTransport(sessionID, "localhost:5556")
	assert.NoError(t, err)
	assert.NotNil(t, transport)
	s.Close()
}
