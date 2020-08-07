package sfu

import (
	"testing"
	"time"

	"github.com/pion/ion-sfu/pkg/relay"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

func TestRelayTransportOpenClose(t *testing.T) {
	sessionID := uint32(1)
	session := NewSession(sessionID)
	client := relay.NewClient(sessionID, "localhost:5558")
	assert.NotNil(t, client)

	relay, err := NewRelayTransport(session, client)
	assert.NoError(t, err)

	relay.Close()
}

func TestRelayTransportSend(t *testing.T) {
	sessionID := uint32(1)
	session := NewSession(sessionID)
	server := relay.NewServer(5559)
	assert.NotNil(t, server)
	client := relay.NewClient(sessionID, "localhost:5559")
	assert.NotNil(t, client)

	relay, err := NewRelayTransport(session, client)
	assert.NoError(t, err)

	assert.NotNil(t, relay.ID())

	done := make(chan struct{})
	go func() {
		conn := server.AcceptSession()
		assert.Equal(t, conn.ID, sessionID)
		close(done)
	}()

	ssrc := uint32(5000)
	track, err := webrtc.NewTrack(webrtc.DefaultPayloadTypeOpus, ssrc, "audio", "pion", webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, 48000))
	assert.NoError(t, err)

	sender, err := relay.NewSender(track)
	assert.NoError(t, err)

	sendRTPWithSenderUntilDone(done, t, track, sender)
	relay.Close()
}

func TestRelayTransportReceive(t *testing.T) {
	sessionID := uint32(1)
	session := NewSession(sessionID)
	server := relay.NewServer(5560)
	assert.NotNil(t, server)
	client := relay.NewClient(sessionID, "localhost:5560")
	assert.NotNil(t, client)

	relayA, err := NewRelayTransport(session, client)
	assert.NoError(t, err)

	assert.NotNil(t, relayA.ID())

	trackA, err := webrtc.NewTrack(webrtc.DefaultPayloadTypeOpus, 5000, "audio", "pion", webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, 48000))
	assert.NoError(t, err)

	trackB, err := webrtc.NewTrack(webrtc.DefaultPayloadTypeOpus, 5001, "audio", "pion", webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, 48000))
	assert.NoError(t, err)

	senderA, err := relayA.NewSender(trackA)
	assert.NoError(t, err)

	done := make(chan struct{})

	go sendRTPWithSenderUntilDone(done, t, trackA, senderA)

	go func() {
		conn := server.AcceptSession()
		assert.Equal(t, conn.ID, sessionID)

		relayB, err := NewRelayTransport(session, conn)
		assert.NoError(t, err)

		senderB, err := relayB.NewSender(trackA)
		assert.NoError(t, err)

		go func() {
			for {
				if relayA.GetRouter(trackB.SSRC()) != nil && relayB.GetRouter(trackA.SSRC()) != nil {
					close(done)
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
		}()

		sendRTPWithSenderUntilDone(done, t, trackB, senderB)
	}()

	<-done
	relayA.Close()
}
