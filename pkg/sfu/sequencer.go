package sfu

import (
	"encoding/binary"
	"sync"
	"time"

	log "github.com/pion/ion-log"
)

const (
	packetMetaSize       = 16
	maxPacketMetaHistory = 500

	ignoreRetransmission = 100 // Ignore packet retransmission after ignoreRetransmission milliseconds
)

type packetMeta []byte

// Returns the original sequence number from stream.
// The original sequence number is used to find the original
// packet from publisher
func (p packetMeta) getSourceSeqNo() uint16 {
	return binary.BigEndian.Uint16(p[0:2])
}

// Returns the modified sequence number after offset.
// This sequence number is used for the associated
// down track, is modified according the offsets, and
// must not be shared
func (p packetMeta) getTargetSeqNo() uint16 {
	return binary.BigEndian.Uint16(p[2:4])
}

// Returns the modified timestamp for current associated
// down track.
func (p packetMeta) getTimestamp() uint32 {
	return binary.BigEndian.Uint32(p[4:8])
}

// Last nack returns the last time this packet was nack requested.
// Sometimes clients request the same packet more than once, so keep
// track of the requested packets helps to avoid writing multiple times
// the same packet.
// The resolution is 1 ms counting after the sequencer start time.
func (p packetMeta) getLastNack() uint32 {
	return binary.BigEndian.Uint32(p[8:12])
}

// Sets the last time this packet was requested by a nack.
func (p packetMeta) setLastNack(tm uint32) {
	binary.BigEndian.PutUint32(p[8:12], tm)
}

// Returns the getLayer from this packet belongs.
func (p packetMeta) getLayer() uint8 {
	return p[13]
}

func (p packetMeta) setVP8PayloadMeta(tlz0Idx uint8, picID uint16) {
	p[14] = tlz0Idx
	binary.BigEndian.PutUint16(p[14:16], picID)
}

func (p packetMeta) getVP8PayloadMeta() (uint8, uint16) {
	return p[14], binary.BigEndian.Uint16(p[14:16])
}

// Sequencer stores the packet sequence received by the down track
type sequencer struct {
	sync.Mutex
	init      bool
	seq       []byte
	step      int
	headSN    uint16
	startTime int64
}

func newSequencer() *sequencer {
	return &sequencer{
		startTime: time.Now().UnixNano() / 1e6,
		seq:       make([]byte, packetMetaSize*maxPacketMetaHistory),
	}
}

func (n *sequencer) push(sn, offSn uint16, timeStamp uint32, layer uint8, head bool) packetMeta {
	n.Lock()
	defer n.Unlock()
	if !n.init {
		n.headSN = offSn
		n.init = true
	}

	step := 0
	if head {
		inc := offSn - n.headSN
		for i := uint16(1); i < inc; i++ {
			n.step++
			if n.step >= maxPacketMetaHistory {
				n.step = 0
			}
		}
		step = n.step
		n.headSN = offSn
	} else {
		step = n.step - int(n.headSN-offSn)
		if step < 0 {
			if step*-1 >= maxPacketMetaHistory {
				log.Warnf("old packet received, can not be sequenced, head: %d received: %d", sn, offSn)
				return packetMeta{}
			}
			step = maxPacketMetaHistory + step
		}
	}
	off := step * packetMetaSize
	binary.BigEndian.PutUint16(n.seq[off:off+2], sn)
	binary.BigEndian.PutUint16(n.seq[off+2:off+4], offSn)
	binary.BigEndian.PutUint32(n.seq[off+4:off+8], timeStamp)
	n.seq[off+13] = layer
	n.step++
	if n.step >= maxPacketMetaHistory {
		n.step = 0
	}
	return n.seq[off : off+packetMetaSize]
}

func (n *sequencer) getSeqNoPairs(seqNo []uint16) []packetMeta {
	n.Lock()
	meta := make([]packetMeta, 0, 17)
	refTime := uint32(time.Now().UnixNano()/1e6 - n.startTime)
	for _, sn := range seqNo {
		step := n.step - int(n.headSN-sn) - 1
		if step < 0 {
			if step*-1 >= maxPacketMetaHistory {
				continue
			}
			step = maxPacketMetaHistory + step
		}
		off := step * packetMetaSize
		seq := packetMeta(n.seq[off : off+packetMetaSize])
		if seq.getTargetSeqNo() == sn {
			tm := seq.getLastNack()
			if tm == 0 || refTime-tm > ignoreRetransmission {
				seq.setLastNack(refTime)
				meta = append(meta, seq)
			}
		}
	}
	n.Unlock()
	return meta
}
