package sfu

import (
	"encoding/binary"
	"strings"
	"sync/atomic"

	"github.com/pion/ion-sfu/pkg/buffer"
	"github.com/pion/webrtc/v3"
)

const (
	ntpEpoch      = 2208988800
	maxVP8PID     = 127
	maxVP8PIDMBit = 32767
)

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

// setVp8TemporalLayer is a helper to detect and modify accordingly the vp8 payload to reflect
// temporal changes in the SFU.
// VP8 temporal layers implemented according https://tools.ietf.org/html/rfc7741
func setVP8TemporalLayer(p buffer.ExtPacket, sn uint16, s *DownTrack) (payload []byte, drop bool) {
	pkt, ok := p.Payload.(buffer.VP8)
	if !ok {
		return p.Packet.Payload, false
	}

	// Check if temporal layer is requested
	if s.simulcast.targetTempLayer != s.simulcast.currentTempLayer {
		if pkt.TID <= uint8(s.simulcast.targetTempLayer) {
			s.simulcast.currentTempLayer = s.simulcast.targetTempLayer
		}
	} else if pkt.TID > uint8(s.simulcast.currentTempLayer) {
		drop = true
		return
	}
	// If we are here modify payload
	payload = packetFactory.Get().([]byte)
	payload = payload[:len(p.Packet.Payload)]
	copy(payload, p.Packet.Payload)
	picID := uint16(0)
	if p.Head {
		s.simulcast.refPicID++
		if pkt.MBit && s.simulcast.refPicID > maxVP8PIDMBit {
			s.simulcast.refPicID = 0
		}
		if !pkt.MBit && s.simulcast.refPicID > maxVP8PID {
			s.simulcast.refPicID = 0
		}
		s.simulcast.refSN = sn
		picID = s.simulcast.refPicID
	} else {
		diff := s.simulcast.refSN - sn
		refOff := int(s.simulcast.refPicID) - int(diff)
		if refOff < 0 {
			if pkt.MBit {
				picID = uint16(maxVP8PIDMBit + refOff)
			} else {
				picID = uint16(maxVP8PID + refOff)
			}
		} else {
			picID = uint16(refOff)
		}
	}

	if pkt.PicIDIdx > 0 {
		pid := make([]byte, 2)
		binary.BigEndian.PutUint16(pid, picID)
		payload[pkt.PicIDIdx] = pid[0]
		if pkt.MBit {
			payload[pkt.PicIDIdx] |= 0x80
			payload[pkt.PicIDIdx+1] = pid[1]
		}
	}
	return
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

func ntpToMillisSinceEpoch(ntp uint64) uint64 {
	// ntp time since epoch calculate fractional ntp as milliseconds
	// (lower 32 bits stored as 1/2^32 seconds) and add
	// ntp seconds (stored in higher 32 bits) as milliseconds
	return (((ntp & 0xFFFFFFFF) * 1000) >> 32) + ((ntp >> 32) * 1000)
}

func fastForwardTimestampAmount(newestTimestamp uint32, referenceTimestamp uint32) uint32 {
	if buffer.IsTimestampWrapAround(newestTimestamp, referenceTimestamp) {
		return uint32(uint64(newestTimestamp) + 0x100000000 - uint64(referenceTimestamp))
	}
	if newestTimestamp < referenceTimestamp {
		return 0
	}
	return newestTimestamp - referenceTimestamp
}
