package sfu

import (
	"encoding/binary"
	"errors"
)

var (
	errShortPacket = errors.New("packet is not large enough")
	errNilPacket   = errors.New("invalid nil packet")
)

const (
	tl0picidxMax = 32767
)

// VP8TempHelper vp8 helper to get temporal data from header
type VP8TempHelper struct {
	TemporalSupported bool
	// Optional Header
	PictureID uint16 /* 8 or 16 bits, picture ID */
	TL0PICIDX uint8  /* 8 bits temporal level zero index */

	// Optional Header If either of the T or K bits are set to 1,
	// the TID/Y/KEYIDX extension field MUST be present.
	TID uint8 /* 2 bits temporal layer idx*/

	IsKeyFrame bool
}

// Unmarshal parses the passed byte slice and stores the result in the VP8TempHelper this method is called upon
func (p *VP8TempHelper) Unmarshal(pl []byte) error {
	if pl == nil {
		return errNilPacket
	}

	payloadLen := len(pl)

	if payloadLen < 4 {
		return errShortPacket
	}

	idx := 0
	// Check for extended bit control
	if pl[idx]&0x80 > 0 {
		idx++
		// Check if T or K is present, if not, no temporal layer is available
		p.TemporalSupported = pl[idx]&0x20 == 1
		L := pl[idx]&0x40 > 0
		// Check for PictureID
		if pl[idx]&0x80 > 0 {
			idx++
			pid := []byte{pl[idx] & 0x7f}
			// Check if m is 1, then Picture ID is 16 bit
			if pl[idx]&0x80 > 0 {
				idx++
				pid = append(pid, pl[idx])
			}
			p.PictureID = binary.BigEndian.Uint16(pid)
		}
		// Check if TL0PICIDX is present
		if L {
			p.TL0PICIDX = pl[idx]
			idx++
		}
		// Set TID
		p.TID = (pl[idx] & 0xc0) >> 6
	}
	if idx >= payloadLen {
		return errShortPacket
	}
	p.IsKeyFrame = pl[idx : idx+1][0]&0x01 == 0
	return nil
}

// setVp8TemporalLayer is a helper to detect and modify accordingly the vp8 payload to reflect
// temporal changes in the SFU.
// VP8 temporal layers implemented according https://tools.ietf.org/html/rfc7741
func setVP8TemporalLayer(pl []byte, s *WebRTCSimulcastSender) (payload []byte, skip bool) {
	var (
		idx          uint8
		currentPicID uint16
		mBit         bool
		picIDIdx     uint8
		tlzIdx       uint8
		currentTlzi  uint8
	)
	// Check for extended bit control
	if pl[idx]&0x80 > 0 {
		idx++
		// Check if T or K is present, if not, no temporal layer is available
		if pl[idx]&0x20 == 0 && pl[idx]&0x10 == 0 {
			return
		}
		L := pl[idx]&0x40 > 0
		// Check for PictureID
		if pl[idx]&0x80 > 0 {
			idx++
			picIDIdx = idx
			pid := []byte{pl[idx] & 0x7f}
			// Check if m is 1, then Picture ID is 15 bits
			if pl[idx]&0x80 > 0 {
				mBit = true
				idx++
				pid = append(pid, pl[idx])
			}
			currentPicID = binary.BigEndian.Uint16(pid)
		}
		// Check if TL0PICIDX is present
		if L {
			idx++
			tlzIdx = idx
			currentTlzi = pl[idx]
		}
		// Check if temporal layer is requested
		if ((pl[idx] & 0xc0) >> 6) > s.currentTempLayer {
			skip = true
			s.refTlzi++
			s.refPicId++
			return
		}
		// If we are here modify payload
		payload = make([]byte, len(pl))
		copy(payload, pl)
		// Modify last zero index
		if L {
			s.lastTlzi = currentTlzi - s.refTlzi
			payload[tlzIdx] = s.lastTlzi
		}
		if picIDIdx > 0 {
			s.lastPicId = currentPicID - s.refPicId
			pid := make([]byte, 2)
			binary.BigEndian.PutUint16(pid, s.lastPicId)
			payload[picIDIdx] = pid[0]
			if mBit {
				payload[picIDIdx] |= 0x80
				payload[picIDIdx+1] = pid[1]
			}
		}
	}
	return
}
