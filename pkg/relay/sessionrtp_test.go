package relay

import (
	"net"
	"testing"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
)

func buildSessionRTPPair(t *testing.T) (*SessionRTP, *SessionRTP) {
	aPipe, bPipe := net.Pipe()
	sa, err := NewSessionRTP(aPipe)
	assert.NoError(t, err)
	sb, err := NewSessionRTP(bPipe)
	assert.NoError(t, err)
	return sa, sb
}

func TestNewSessionRTP(t *testing.T) {
	const (
		ssrc          = uint32(5000)
		rtpHeaderSize = 12
	)
	rtpPkt := &rtp.Packet{
		Header:  rtp.Header{SSRC: ssrc, CSRC: []uint32{}},
		Payload: []byte{0x00, 0x01, 0x03, 0x04},
	}
	rtpRaw, err := rtpPkt.Marshal()
	assert.NoError(t, err)

	readBuffer := make([]byte, len(rtpRaw))
	aSession, bSession := buildSessionRTPPair(t)

	aWriteStream, err := aSession.OpenWriteStream()
	assert.NoError(t, err)
	_, err = aWriteStream.WriteRTP(rtpPkt)
	assert.NoError(t, err)

	bReadStream, err := bSession.AcceptStream()
	assert.NoError(t, err)
	assert.Equal(t, bReadStream.ID(), ssrc)

	_, pkt, err := bReadStream.ReadRTP(readBuffer)
	assert.NoError(t, err)
	assert.Equal(t, rtpRaw, readBuffer)
	assert.Equal(t, rtpPkt, pkt)

	err = bReadStream.Close()
	assert.NoError(t, err)
	err = aSession.Close()
	assert.NoError(t, err)
	err = bSession.Close()
	assert.NoError(t, err)
}
