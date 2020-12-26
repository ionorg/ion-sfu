package buffer

import (
	"sort"

	"github.com/pion/rtcp"
)

const maxNackTimes = 3   // Max number of times a packet will be NACKed
const maxNackCache = 100 // Max NACK sn the sfu will keep reference

type nack struct {
	sn     uint32
	nacked uint8
}

type nackQueue struct {
	nacks   []nack
	counter uint8
	maxSN   uint16
	kfSN    uint32
	cycles  uint32
}

func newNACKQueue() *nackQueue {
	return &nackQueue{
		nacks:  make([]nack, 0, maxNackCache+1),
		maxSN:  0,
		cycles: 0,
	}
}

func (n *nackQueue) reset() {
	n.maxSN = 0
	n.counter = 0
	n.cycles = 0
	n.kfSN = 0
	n.nacks = n.nacks[:0]
}

func (n *nackQueue) remove(sn uint16) {
	var extSN uint32
	if sn > n.maxSN && sn&0x8000 == 1 && n.maxSN&0x8000 == 0 {
		extSN = (n.cycles - maxSN) | uint32(sn)
	} else {
		extSN = n.cycles | uint32(sn)
	}

	i := sort.Search(len(n.nacks), func(i int) bool { return n.nacks[i].sn >= extSN })
	if i > len(n.nacks) || n.nacks[i].sn != extSN {
		return
	}
	copy(n.nacks[i:], n.nacks[i+1:])
	n.nacks = n.nacks[:len(n.nacks)-1]
}

func (n *nackQueue) push(sn uint16) {
	var extSN uint32
	if n.maxSN == 0 {
		n.maxSN = sn
	} else if (sn-n.maxSN)&0x8000 == 0 {
		if sn < n.maxSN {
			n.cycles += maxSN
		}
		n.maxSN = sn
	}

	if sn > n.maxSN && sn&0x8000 == 1 && n.maxSN&0x8000 == 0 {
		extSN = (n.cycles - maxSN) | uint32(sn)
	} else {
		extSN = n.cycles | uint32(sn)
	}

	i := sort.Search(len(n.nacks), func(i int) bool { return n.nacks[i].sn >= extSN })
	if i < len(n.nacks) && n.nacks[i].sn == extSN {
		return
	}
	n.nacks = append(n.nacks, nack{})
	copy(n.nacks[i+1:], n.nacks[i:])
	n.nacks[i] = nack{
		sn:     extSN,
		nacked: 0,
	}

	if len(n.nacks) > maxNackCache {
		n.nacks = n.nacks[1:]
	}
	n.counter++
}

func (n *nackQueue) pairs() ([]rtcp.NackPair, bool) {
	if n.counter < 2 {
		n.counter++
		return nil, false
	}
	n.counter = 0
	i := 0
	askKF := false
	var np rtcp.NackPair
	var nps []rtcp.NackPair
	for _, nck := range n.nacks {
		if nck.nacked >= maxNackTimes {
			if nck.sn > n.kfSN {
				n.kfSN = nck.sn
				askKF = true
			}
			continue
		}
		n.nacks[i] = nack{
			sn:     nck.sn,
			nacked: nck.nacked + 1,
		}
		i++
		if np.PacketID == 0 || uint16(nck.sn) > np.PacketID+16 {
			if np.PacketID != 0 {
				nps = append(nps, np)
			}
			np.PacketID = uint16(nck.sn)
			np.LostPackets = 0
			continue
		}
		np.LostPackets |= 1 << (uint16(nck.sn) - np.PacketID - 1)
	}
	if np.PacketID != 0 {
		nps = append(nps, np)
	}
	n.nacks = n.nacks[:i]
	return nps, askKF
}
