package relay

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBasic(t *testing.T) {
	p := &Packet{}

	if err := p.Unmarshal([]byte{}); err == nil {
		t.Fatal("Unmarshal did not error on zero length packet")
	}

	rawPkt := []byte{
		0x40, 0x00, 0x00, 0x00, 0x1c, 0x64, 0x27, 0x82, 0x90, 0xe0, 0x69, 0x8f, 0xd9, 0xc2, 0x93,
		0xda, 0x1c, 0x64, 0x27, 0x82, 0x00, 0x01, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x98, 0x36,
		0xbe, 0x88, 0x9e,
	}

	parsedPacket := &Packet{
		Header: Header{
			Version:   1,
			SessionID: 476325762,
		},
		Payload: rawPkt[8:],
		Raw:     rawPkt,
	}

	err := p.Unmarshal(rawPkt)
	assert.NoError(t, err)
	assert.Equal(t, p, parsedPacket)
	assert.Equal(t, parsedPacket.Header.MarshalSize(), 8)
	assert.Equal(t, parsedPacket.MarshalSize(), len(rawPkt))

	raw, err := p.Marshal()
	assert.NoError(t, err)
	assert.Equal(t, raw, rawPkt)
}
