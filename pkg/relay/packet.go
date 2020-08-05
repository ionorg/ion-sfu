package relay

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	errInvalidHeader = errors.New("relay: invalid header")
)

/*
 *  0                   1                   2                   3
 *  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
 * | V | M |                   reserved                            |
 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
 * |                        conference id                          |
 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
 */

// Header represents a relay packet header
type Header struct {
	Version   uint8
	MediaType uint8
	SessionID uint32
}

// Packet represents a relay packet
type Packet struct {
	Header
	Raw     []byte
	Payload []byte
}

const (
	headerLength   = 2
	versionShift   = 6
	versionMask    = 0x3
	mediaTypeShift = 4
	mediaTypeMask  = 0x3
)

// Unmarshal parses the passed byte slice and stores the result in the Header this method is called upon
func (h *Header) Unmarshal(rawPacket []byte) error {
	if len(rawPacket) < headerLength {
		return fmt.Errorf("Relay header size insufficient; %d < %d", len(rawPacket), headerLength)
	}

	h.Version = rawPacket[0] >> versionShift & versionMask
	h.MediaType = rawPacket[0] >> mediaTypeShift & mediaTypeMask
	h.SessionID = binary.BigEndian.Uint32(rawPacket[4:8])

	return nil
}

// Unmarshal parses the passed byte slice and stores the result in the Packet this method is called upon
func (p *Packet) Unmarshal(rawPacket []byte) error {
	if err := p.Header.Unmarshal(rawPacket); err != nil {
		return err
	}

	p.Payload = rawPacket[8:]
	p.Raw = rawPacket
	return nil
}

// // Marshal serializes the header into bytes.
// func (h *Header) Marshal() (buf []byte, err error) {
// 	buf = make([]byte, h.MarshalSize())
// 	n, err := h.MarshalTo(buf)
// 	if err != nil {
// 		return nil, err
// 	}

// 	return buf[:n], nil
// }

// MarshalTo serializes the header and writes to the buffer.
func (h *Header) MarshalTo(buf []byte) (n int, err error) {
	size := h.MarshalSize()
	if size > len(buf) {
		return 0, io.ErrShortBuffer
	}

	// The first byte contains the version and media type
	buf[0] = (h.Version << versionShift)
	buf[0] |= 1 << mediaTypeShift

	binary.BigEndian.PutUint32(buf[4:8], h.SessionID)

	return 8, nil
}

// MarshalSize returns the size of the header once marshaled.
func (h *Header) MarshalSize() int {
	return 8
}

// Marshal serializes the packet into bytes.
func (p *Packet) Marshal() (buf []byte, err error) {
	buf = make([]byte, p.MarshalSize())

	n, err := p.MarshalTo(buf)
	if err != nil {
		return nil, err
	}

	return buf[:n], nil
}

// MarshalTo serializes the packet and writes to the buffer.
func (p *Packet) MarshalTo(buf []byte) (n int, err error) {
	n, err = p.Header.MarshalTo(buf)
	if err != nil {
		return 0, err
	}

	// Make sure the buffer is large enough to hold the packet.
	if n+len(p.Payload) > len(buf) {
		return 0, io.ErrShortBuffer
	}

	m := copy(buf[n:], p.Payload)
	p.Raw = buf[:n+m]

	return n + m, nil
}

// MarshalSize returns the size of the packet once marshaled.
func (p *Packet) MarshalSize() int {
	return p.Header.MarshalSize() + len(p.Payload)
}

// Unmarshal takes an entire udp datagram (which may consist of multiple relay packets) and
// returns the unmarshaled packets it contains.
// func Unmarshal(rawData []byte) ([]Packet, error) {
// 	var packets []Packet
// 	for len(rawData) != 0 {
// 		p := Packet{}
// 		p.Unmarshal(rawData)

// 		if err != nil {
// 			return nil, err
// 		}

// 		packets = append(packets, p)
// 		rawData = rawData[processed:]
// 	}

// 	switch len(packets) {
// 	// Empty packet
// 	case 0:
// 		return nil, errInvalidHeader
// 	// Multiple Packets
// 	default:
// 		return packets, nil
// 	}
// }
