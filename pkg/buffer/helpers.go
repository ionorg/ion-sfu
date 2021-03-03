package buffer

import (
	"encoding/binary"
	"errors"
	"sync/atomic"
)

var (
	errShortPacket = errors.New("packet is not large enough")
	errNilPacket   = errors.New("invalid nil packet")
)

type atomicBool int32

func (a *atomicBool) set(value bool) {
	var i int32
	if value {
		i = 1
	}
	atomic.StoreInt32((*int32)(a), i)
}

func (a *atomicBool) get() bool {
	return atomic.LoadInt32((*int32)(a)) != 0
}

// VP8 is a helper to get temporal data from VP8 packet header
/*
	VP8 Payload Descriptor
			0 1 2 3 4 5 6 7                      0 1 2 3 4 5 6 7
			+-+-+-+-+-+-+-+-+                   +-+-+-+-+-+-+-+-+
			|X|R|N|S|R| PID | (REQUIRED)        |X|R|N|S|R| PID | (REQUIRED)
			+-+-+-+-+-+-+-+-+                   +-+-+-+-+-+-+-+-+
		X:  |I|L|T|K| RSV   | (OPTIONAL)   X:   |I|L|T|K| RSV   | (OPTIONAL)
			+-+-+-+-+-+-+-+-+                   +-+-+-+-+-+-+-+-+
		I:  |M| PictureID   | (OPTIONAL)   I:   |M| PictureID   | (OPTIONAL)
			+-+-+-+-+-+-+-+-+                   +-+-+-+-+-+-+-+-+
		L:  |   TL0PICIDX   | (OPTIONAL)        |   PictureID   |
			+-+-+-+-+-+-+-+-+                   +-+-+-+-+-+-+-+-+
		T/K:|TID|Y| KEYIDX  | (OPTIONAL)   L:   |   TL0PICIDX   | (OPTIONAL)
			+-+-+-+-+-+-+-+-+                   +-+-+-+-+-+-+-+-+
		T/K:|TID|Y| KEYIDX  | (OPTIONAL)
			+-+-+-+-+-+-+-+-+
*/
type VP8 struct {
	TemporalSupported bool
	// Optional Header
	PictureID uint16 /* 8 or 16 bits, picture ID */
	PicIDIdx  int
	MBit      bool
	TL0PICIDX uint8 /* 8 bits temporal level zero index */
	TlzIdx    int

	// Optional Header If either of the T or K bits are set to 1,
	// the TID/Y/KEYIDX extension field MUST be present.
	TID uint8 /* 2 bits temporal layer idx*/
	// IsKeyFrame is a helper to detect if current packet is a keyframe
	IsKeyFrame bool
}

// Unmarshal parses the passed byte slice and stores the result in the VP8 this method is called upon
func (p *VP8) Unmarshal(payload []byte) error {
	if payload == nil {
		return errNilPacket
	}

	payloadLen := len(payload)

	if payloadLen < 4 {
		return errShortPacket
	}

	idx := 0
	S := payload[idx]&0x10 > 0
	// Check for extended bit control
	if payload[idx]&0x80 > 0 {
		idx++
		// Check if T is present, if not, no temporal layer is available
		p.TemporalSupported = payload[idx]&0x20 > 0
		K := payload[idx]&0x10 > 0
		L := payload[idx]&0x40 > 0
		// Check for PictureID
		if payload[idx]&0x80 > 0 {
			idx++
			p.PicIDIdx = idx
			pid := payload[idx] & 0x7f
			// Check if m is 1, then Picture ID is 15 bits
			if payload[idx]&0x80 > 0 {
				idx++
				p.MBit = true
				p.PictureID = binary.BigEndian.Uint16([]byte{pid, payload[idx]})
			} else {
				p.PictureID = uint16(pid)
			}
		}
		// Check if TL0PICIDX is present
		if L {
			idx++
			p.TlzIdx = idx
			p.TL0PICIDX = payload[idx]
		}
		if p.TemporalSupported || K {
			idx++
			p.TID = (payload[idx] & 0xc0) >> 6
		}
		if int(idx) >= payloadLen {
			return errShortPacket
		}
		idx++
		// Check is packet is a keyframe by looking at P bit in vp8 payload
		p.IsKeyFrame = payload[idx]&0x01 == 0 && S
	} else {
		idx++
		// Check is packet is a keyframe by looking at P bit in vp8 payload
		p.IsKeyFrame = payload[idx]&0x01 == 0 && S
	}
	return nil
}

// isH264Keyframe detects if h264 payload is a keyframe
// this code was taken from https://github.com/jech/galene/blob/codecs/rtpconn/rtpreader.go#L45
// all credits belongs to Juliusz Chroboczek @jech and the awesome Galene SFU
func isH264Keyframe(payload []byte) bool {
	if len(payload) < 1 {
		return false
	}
	nalu := payload[0] & 0x1F
	if nalu == 0 {
		// reserved
		return false
	} else if nalu <= 23 {
		// simple NALU
		return nalu == 5
	} else if nalu == 24 || nalu == 25 || nalu == 26 || nalu == 27 {
		// STAP-A, STAP-B, MTAP16 or MTAP24
		i := 1
		if nalu == 25 || nalu == 26 || nalu == 27 {
			// skip DON
			i += 2
		}
		for i < len(payload) {
			if i+2 > len(payload) {
				return false
			}
			length := uint16(payload[i])<<8 |
				uint16(payload[i+1])
			i += 2
			if i+int(length) > len(payload) {
				return false
			}
			offset := 0
			if nalu == 26 {
				offset = 3
			} else if nalu == 27 {
				offset = 4
			}
			if offset >= int(length) {
				return false
			}
			n := payload[i+offset] & 0x1F
			if n == 5 {
				return true
			} else if n >= 24 {
				// is this legal?
				Logger.V(0).Info("Non-simple NALU within a STAP")
			}
			i += int(length)
		}
		if i == len(payload) {
			return false
		}
		return false
	} else if nalu == 28 || nalu == 29 {
		// FU-A or FU-B
		if len(payload) < 2 {
			return false
		}
		if (payload[1] & 0x80) == 0 {
			// not a starting fragment
			return false
		}
		return payload[1]&0x1F == 5
	}
	return false
}
