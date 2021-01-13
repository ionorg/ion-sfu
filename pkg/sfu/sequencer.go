package sfu

import (
	"sync"
	"time"
)

const (
	ignoreRetransmission = 50 // Ignore packet retransmission after ignoreRetransmission milliseconds
	maxSequencerSize     = 256
)

type NACK struct {
	SN  uint16
	LRX int64
}

// Sequencer stores the packet sequence received by the down track in the following format:
// - 16 bits modified sequence number
// - 16 bits original sequence number
// - 32 bits last nack timestamp 1 ms counting after time start
type sequencer struct {
	sync.RWMutex
	seq       [maxSequencerSize]uint64
	step      uint8
	headSN    uint16
	startTime int64
}

func newSequencer() *sequencer {
	return &sequencer{
		startTime: time.Now().UnixNano() / 1e6,
	}
}

func (n *sequencer) push(sn, offSn uint16, head bool) {
	n.Lock()
	if n.headSN == 0 {
		n.headSN = offSn
	}

	step := uint8(0)
	if head {
		inc := offSn - n.headSN
		for i := uint16(1); i < inc; i++ {
			n.step++
		}
		n.step++
		step = n.step
		n.headSN = offSn
	} else {
		step = n.step - uint8(n.headSN-offSn)
	}

	n.seq[step] = uint64(sn)<<16 | uint64(offSn)
	n.Unlock()
}

func (n *sequencer) getNACKSeqNo(seqNo []uint16) []uint32 {
	n.Lock()
	packets := make([]uint32, 0, 17)
	refTime := time.Now().UnixNano()/1e6 - n.startTime
	for _, sn := range seqNo {
		step := n.step - uint8(n.headSN-sn)
		seq := n.seq[step]
		if uint16(seq) == sn {
			tm := int64(seq >> 32)
			if tm == 0 || refTime-tm > ignoreRetransmission {
				packets = append(packets, uint32(seq))
				n.seq[step] = uint64(refTime)<<32 | uint64(uint32(seq))
			}
		}
	}
	n.Unlock()
	return packets
}
