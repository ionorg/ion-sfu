package sfu

import (
	"sync"
	"time"
)

const (
	ignoreRetransmission = 100 // Ignore packet retransmission after ignoreRetransmission milliseconds
)

type packetMeta struct {
	// Original sequence number from stream.
	// The original sequence number is used to find the original
	// packet from publisher
	sourceSeqNo uint16
	// Modified sequence number after offset.
	// This sequence number is used for the associated
	// down track, is modified according the offsets, and
	// must not be shared
	targetSeqNo uint16
	// Modified timestamp for current associated
	// down track.
	timestamp uint32
	// The last time this packet was nack requested.
	// Sometimes clients request the same packet more than once, so keep
	// track of the requested packets helps to avoid writing multiple times
	// the same packet.
	// The resolution is 1 ms counting after the sequencer start time.
	lastNack uint32
	// Spatial layer of packet
	layer uint8
	// Information that differs depending the codec
	misc uint32
}

func (p packetMeta) setVP8PayloadMeta(tlz0Idx uint8, picID uint16) {
	p.misc = uint32(tlz0Idx)<<16 | uint32(picID)
}

func (p packetMeta) getVP8PayloadMeta() (uint8, uint16) {
	return uint8(p.misc >> 16), uint16(p.misc)
}

// Sequencer stores the packet sequence received by the down track
type sequencer struct {
	sync.Mutex
	init      bool
	max       int
	seq       []packetMeta
	step      int
	headSN    uint16
	startTime int64
}

func newSequencer(maxTrack int) *sequencer {
	return &sequencer{
		startTime: time.Now().UnixNano() / 1e6,
		max:       maxTrack,
		seq:       make([]packetMeta, maxTrack),
	}
}

func (n *sequencer) push(sn, offSn uint16, timeStamp uint32, layer uint8, head bool) *packetMeta {
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
			if n.step >= n.max {
				n.step = 0
			}
		}
		step = n.step
		n.headSN = offSn
	} else {
		step = n.step - int(n.headSN-offSn)
		if step < 0 {
			if step*-1 >= n.max {
				Logger.V(0).Info("Old packet received, can not be sequenced", "head", sn, "received", offSn)
				return nil
			}
			step = n.max + step
		}
	}
	n.seq[n.step] = packetMeta{
		sourceSeqNo: sn,
		targetSeqNo: offSn,
		timestamp:   timeStamp,
		layer:       layer,
	}
	n.step++
	if n.step >= n.max {
		n.step = 0
	}
	return &n.seq[n.step]
}

func (n *sequencer) getSeqNoPairs(seqNo []uint16) []packetMeta {
	n.Lock()
	meta := make([]packetMeta, 0, 17)
	refTime := uint32(time.Now().UnixNano()/1e6 - n.startTime)
	for _, sn := range seqNo {
		step := n.step - int(n.headSN-sn) - 1
		if step < 0 {
			if step*-1 >= n.max {
				continue
			}
			step = n.max + step
		}
		seq := &n.seq[step]
		if seq.targetSeqNo == sn {
			if seq.lastNack == 0 || refTime-seq.lastNack > ignoreRetransmission {
				seq.lastNack = refTime
				meta = append(meta, *seq)
			}
		}
	}
	n.Unlock()
	return meta
}
