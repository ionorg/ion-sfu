package relay

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func buildSessionRelayPair(t *testing.T) (*SessionRelay, *SessionRelay) {
	aPipe, bPipe := net.Pipe()
	sa, err := NewSessionRelay(aPipe)
	assert.NoError(t, err)
	sb, err := NewSessionRelay(bPipe)
	assert.NoError(t, err)
	return sa, sb
}

func TestNewSessionRelay(t *testing.T) {
	rtpRaw := []byte{
		0x90, 0xe0, 0x69, 0x8f, 0xd9, 0xc2, 0x93, 0xda, 0x1c, 0x64,
		0x27, 0x82, 0x00, 0x01, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x98, 0x36, 0xbe, 0x88, 0x9e,
	}

	sessionID := uint32(2)
	relay := &Packet{
		Header: Header{
			Version:   1,
			SessionID: sessionID,
		},
		Payload: rtpRaw,
	}
	relayRaw, err := relay.Marshal()
	assert.NoError(t, err)

	readBuffer := make([]byte, len(relayRaw))
	aSession, bSession := buildSessionRelayPair(t)
	aWriteStream, err := aSession.OpenWriteStream()
	assert.NoError(t, err)
	_, err = aWriteStream.WriteRelay(relay)
	assert.NoError(t, err)

	bReadStream, err := bSession.AcceptStream()
	assert.NoError(t, err)
	assert.Equal(t, bReadStream.ID(), sessionID)

	// Test stubs
	assert.Nil(t, bReadStream.LocalAddr())
	assert.Nil(t, bReadStream.RemoteAddr())
	assert.Nil(t, bReadStream.SetDeadline(time.Time{}))
	assert.Nil(t, bReadStream.SetReadDeadline(time.Time{}))
	assert.Nil(t, bReadStream.SetWriteDeadline(time.Time{}))

	_, pkt, err := bReadStream.ReadRelay(readBuffer)
	assert.NoError(t, err)
	assert.Equal(t, relayRaw, readBuffer)
	assert.Equal(t, relay, pkt)

	err = bReadStream.Close()
	assert.NoError(t, err)
	err = aSession.Close()
	assert.NoError(t, err)
	err = bSession.Close()
	assert.NoError(t, err)
}
