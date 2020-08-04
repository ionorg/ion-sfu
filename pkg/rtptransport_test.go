package sfu

import (
	"testing"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
)

func TestNewRTPTransport(t *testing.T) {
	rawPkt := []byte{
		0x90, 0xe0, 0x69, 0x8f, 0xd9, 0xc2, 0x93, 0xda, 0x1c, 0x64,
		0x27, 0x82, 0x00, 0x01, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x98, 0x36, 0xbe, 0x88, 0x9e,
	}

	p := &rtp.Packet{}
	rtpTransport := NewOutRTPTransport("awsome", "0.0.0.0:6789")
	err := p.Unmarshal(rawPkt)
	assert.NoError(t, err)
	err = rtpTransport.WriteRTP(p)
	assert.NoError(t, err)
}
