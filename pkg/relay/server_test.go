package relay

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func sendRelayUntilDone(done <-chan struct{}, t *testing.T, id uint32, conn net.Conn) {
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			pkt := &Packet{
				Header: Header{
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

func TestClientServer(t *testing.T) {
	sessionID := uint32(5)
	server := NewServer(5556)
	assert.NotNil(t, server)
	client := NewClient(sessionID, "localhost:5556")
	assert.NotNil(t, client)

	done := make(chan struct{})
	go func() {
		conn := server.AcceptSession()
		assert.Equal(t, conn.ID, sessionID)
		close(done)
	}()

	sendRelayUntilDone(done, t, sessionID, client)
	server.Close()
	client.Close()
}
