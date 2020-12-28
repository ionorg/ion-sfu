package sfu

import (
	"encoding/binary"
	"strings"
	"sync/atomic"

	log "github.com/pion/ion-log"

	"github.com/pion/webrtc/v3"
)

const ntpEpoch = 2208988800

type atomicBool struct {
	val int32
}

func (b *atomicBool) set(value bool) { // nolint: unparam
	var i int32
	if value {
		i = 1
	}

	atomic.StoreInt32(&(b.val), i)
}

func (b *atomicBool) get() bool {
	return atomic.LoadInt32(&(b.val)) != 0
}

// VP8Helper is a helper to get temporal data from VP8 packet header
/*
	VP8Helper Payload Descriptor
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
type VP8Helper struct {
	TemporalSupported bool
	// Optional Header
	PictureID uint16 /* 8 or 16 bits, picture ID */
	picIDIdx  uint8
	mBit      bool
	TL0PICIDX uint8 /* 8 bits temporal level zero index */
	tlzIdx    uint8

	// Optional Header If either of the T or K bits are set to 1,
	// the TID/Y/KEYIDX extension field MUST be present.
	TID uint8 /* 2 bits temporal layer idx*/
	// IsKeyFrame is a helper to detect if current packet is a keyframe
	IsKeyFrame bool
}

// Unmarshal parses the passed byte slice and stores the result in the VP8Helper this method is called upon
func (p *VP8Helper) Unmarshal(payload []byte) error {
	if payload == nil {
		return errNilPacket
	}

	payloadLen := len(payload)

	if payloadLen < 4 {
		return errShortPacket
	}

	var idx uint8
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
			p.picIDIdx = idx
			pid := payload[idx] & 0x7f
			// Check if m is 1, then Picture ID is 15 bits
			if payload[idx]&0x80 > 0 {
				idx++
				p.mBit = true
				p.PictureID = binary.BigEndian.Uint16([]byte{pid, payload[idx]})
			} else {
				p.PictureID = uint16(pid)
			}
		}
		// Check if TL0PICIDX is present
		if L {
			idx++
			p.tlzIdx = idx
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

// setVp8TemporalLayer is a helper to detect and modify accordingly the vp8 payload to reflect
// temporal changes in the SFU.
// VP8Helper temporal layers implemented according https://tools.ietf.org/html/rfc7741
func setVP8TemporalLayer(pl []byte, s *DownTrack) (payload []byte, skip bool) {
	var pkt VP8Helper
	if err := pkt.Unmarshal(pl); err != nil {
		return nil, false
	}
	// Check if temporal layer is requested
	if pkt.TID > uint8(s.simulcast.currentTempLayer) {
		skip = true
		// Increment references to prevent gaps
		s.simulcast.refTlzi++
		s.simulcast.refPicID++
		return
	}
	// If we are here modify payload
	payload = make([]byte, len(pl))
	copy(payload, pl)
	// Modify last zero index
	if pkt.tlzIdx > 0 {
		s.simulcast.lastTlzi = pkt.TL0PICIDX - s.simulcast.refTlzi
		payload[pkt.tlzIdx] = s.simulcast.lastTlzi
	}
	if pkt.picIDIdx > 0 {
		s.simulcast.lastPicID = pkt.PictureID - s.simulcast.refPicID
		pid := make([]byte, 2)
		binary.BigEndian.PutUint16(pid, s.simulcast.lastPicID)
		payload[pkt.picIDIdx] = pid[0]
		if pkt.mBit {
			payload[pkt.picIDIdx] |= 0x80
			payload[pkt.picIDIdx+1] = pid[1]
		}
	}
	return
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
				log.Warnf("Non-simple NALU within a STAP")
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

func timeToNtp(ns int64) uint64 {
	seconds := uint64(ns/1e9 + ntpEpoch)
	fraction := uint64(((ns % 1e9) << 32) / 1e9)
	return seconds<<32 | fraction
}

// Do a fuzzy find for a codec in the list of codecs
// Used for lookup up a codec in an existing list to find a match
func codecParametersFuzzySearch(needle webrtc.RTPCodecParameters, haystack []webrtc.RTPCodecParameters) (webrtc.RTPCodecParameters, error) {
	// First attempt to match on MimeType + SDPFmtpLine
	for _, c := range haystack {
		if strings.EqualFold(c.RTPCodecCapability.MimeType, needle.RTPCodecCapability.MimeType) &&
			c.RTPCodecCapability.SDPFmtpLine == needle.RTPCodecCapability.SDPFmtpLine {
			return c, nil
		}
	}

	// Fallback to just MimeType
	for _, c := range haystack {
		if strings.EqualFold(c.RTPCodecCapability.MimeType, needle.RTPCodecCapability.MimeType) {
			return c, nil
		}
	}

	return webrtc.RTPCodecParameters{}, webrtc.ErrCodecNotFound
}
