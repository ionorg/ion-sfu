package relay

import (
	"net"
	"testing"

	"github.com/pion/rtcp"
	"github.com/stretchr/testify/assert"
)

func buildSessionRTCPPair(t *testing.T) (*SessionRTCP, *SessionRTCP) {
	aPipe, bPipe := net.Pipe()
	sa, err := NewSessionRTCP(aPipe)
	assert.NoError(t, err)
	sb, err := NewSessionRTCP(bPipe)
	assert.NoError(t, err)
	return sa, sb
}

func TestNewSessionRTCP(t *testing.T) {
	ssrc := uint32(5000)
	rtcpPkt := []rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: ssrc}}
	rtcpRaw, err := rtcp.Marshal(rtcpPkt)
	assert.NoError(t, err)

	readBuffer := make([]byte, len(rtcpRaw))
	aSession, bSession := buildSessionRTCPPair(t)
	aWriteStream, err := aSession.OpenWriteStream()
	assert.NoError(t, err)
	_, err = aWriteStream.Write(rtcpRaw)
	assert.NoError(t, err)

	bReadStream, err := bSession.AcceptStream()
	assert.NoError(t, err)
	assert.Equal(t, bReadStream.ID(), ssrc)

	pkt, err := bReadStream.ReadRTCP(readBuffer)
	assert.NoError(t, err)
	assert.Equal(t, rtcpRaw, readBuffer)
	assert.Equal(t, rtcpPkt, pkt)

	err = bReadStream.Close()
	assert.NoError(t, err)
	err = aSession.Close()
	assert.NoError(t, err)
	err = bSession.Close()
	assert.NoError(t, err)
}
