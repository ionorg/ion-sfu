package relay

import (
	"context"
	"testing"
	"time"

	"github.com/pion/ion-sfu/pkg/relay/mux"
	"github.com/stretchr/testify/assert"
)

func sendRelayUntilDone(done <-chan struct{}, t *testing.T, stream *WriteStreamRelay) {
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			pkt := &Packet{
				Header: Header{
					Version:   1,
					SessionID: 2,
				},
				Payload: []byte{0x01, 0x02, 0x03, 0x04},
			}
			_, err := stream.WriteRelay(pkt)
			assert.NoError(t, err)
		case <-done:
			return
		}
	}
}

func TestClientServer(t *testing.T) {
	server := NewServer(5556)
	assert.NotNil(t, server)
	client := NewClient("localhost:5556")
	assert.NotNil(t, client)

	m := mux.NewMux(mux.Config{
		Conn:       client,
		BufferSize: receiveMTU,
	})

	endpoint := m.NewEndpoint(mux.MatchAll)
	session, err := NewSessionRelay(endpoint)
	assert.NoError(t, err)

	onAccept, onAcceptFunc := context.WithCancel(context.Background())
	go func() {
		server.AcceptRelay()
		onAcceptFunc()
	}()

	stream, err := session.OpenWriteStream()
	assert.NoError(t, err)

	sendRelayUntilDone(onAccept.Done(), t, stream)
	server.Close()
	client.Close()
}
