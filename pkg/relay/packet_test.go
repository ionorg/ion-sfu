package relay

import (
	"reflect"
	"testing"
)

func TestBasic(t *testing.T) {
	p := &Packet{}

	if err := p.Unmarshal([]byte{}); err == nil {
		t.Fatal("Unmarshal did not error on zero length packet")
	}

	rawPkt := []byte{
		0x90, 0x00, 0x00, 0x00, 0x1c, 0x64, 0x27, 0x82, 0x90, 0xe0, 0x69, 0x8f, 0xd9, 0xc2, 0x93,
		0xda, 0x1c, 0x64, 0x27, 0x82, 0x00, 0x01, 0x00, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0x98, 0x36,
		0xbe, 0x88, 0x9e,
	}

	parsedPacket := &Packet{
		Header: Header{
			Version:   2,
			MediaType: 1,
			SessionID: 476325762,
		},
		Payload: rawPkt[8:],
		Raw:     rawPkt,
	}

	if err := p.Unmarshal(rawPkt); err != nil {
		t.Error(err)
	} else if !reflect.DeepEqual(p, parsedPacket) {
		t.Errorf("TestBasic unmarshal: got %#v, want %#v", p, parsedPacket)
	}

	if parsedPacket.Header.MarshalSize() != 8 {
		t.Errorf("wrong computed header marshal size")
	} else if parsedPacket.MarshalSize() != len(rawPkt) {
		t.Errorf("wrong computed marshal size")
	}

	raw, err := p.Marshal()
	if err != nil {
		t.Error(err)
	} else if !reflect.DeepEqual(raw, rawPkt) {
		t.Errorf("TestBasic marshal: got %#v, want %#v", raw, rawPkt)
	}
}
