package rtpengine

import (
	"testing"

	sfu "github.com/pion/ion-sfu/pkg"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
)

var rawPkt = []byte{
	0x90, 0xe0, 0x69, 0x8f, 0xd9, 0xc2, 0x93, 0xda, 0x1c, 0x64,
	0x27, 0x82, 0x00, 0x01, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x98, 0x36, 0xbe, 0x88, 0x9e,
}

func TestServe(t *testing.T) {
	ch, err := Serve(6668)
	assert.NoError(t, err)
	out := sfu.NewOutRTPTransport("test", "localhost:6668")
	p := &rtp.Packet{}
	err = p.Unmarshal(rawPkt)
	assert.NoError(t, err)
	out.WriteRTP(p)
	in := <-ch

	assert.NotNil(t, in)
	assert.NotNil(t, out)
	Close()
}

func TestServeWithKCP(t *testing.T) {
	ch, err := ServeWithKCP(6669, "test", "test")
	assert.NoError(t, err)
	out := sfu.NewOutRTPTransportWithKCP("test", "localhost:6669", "test", "test")
	p := &rtp.Packet{}
	err = p.Unmarshal(rawPkt)
	assert.NoError(t, err)
	out.WriteRTP(p)
	in := <-ch

	assert.NotNil(t, in)
	assert.NotNil(t, out)
	Close()
}
