package relay

import (
	"testing"
)

func TestClientServer(t *testing.T) {
	// server := NewServer(5556)
	// client := NewClient("localhost:5556")

	// remote := sfu.NewRelayTransport(client)

	// track, err := webrtc.NewTrack(webrtc.DefaultPayloadTypeH264, 1, "1", "1", webrtc.NewRTPH264Codec(webrtc.DefaultPayloadTypeH264, 90000))
	// assert.NoError(t, err)
	// sender, err := remote.NewSender(track)
	// assert.NoError(t, err)

	// onAccept, onAcceptFunc := context.WithCancel(context.Background())
	// go func() {
	// 	server.Accept()
	// 	onAcceptFunc()
	// }()

	// remote.stats()

	// sendRTPWithSenderUntilDone(onAccept.Done(), t, track, sender)
	// server.Close()
	// client.Close()
}
