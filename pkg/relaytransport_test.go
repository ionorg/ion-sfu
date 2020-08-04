package sfu

import (
	"context"
	"testing"

	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

func TestClientServer(t *testing.T) {
	server := NewRelayServer(5556)
	client := NewRelayClient("localhost:5556")

	remote := NewRelayTransport(client)

	track, err := webrtc.NewTrack(webrtc.DefaultPayloadTypeH264, 1, "1", "1", webrtc.NewRTPH264Codec(webrtc.DefaultPayloadTypeH264, 90000))
	assert.NoError(t, err)
	sender, err := remote.NewSender(track)
	assert.NoError(t, err)

	onAccept, onAcceptFunc := context.WithCancel(context.Background())
	go func() {
		server.Accept()
		onAcceptFunc()
	}()

	remote.stats()

	sendRTPWithSenderUntilDone(onAccept.Done(), t, track, sender)
	server.Close()
	client.Close()
}
