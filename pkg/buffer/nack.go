package buffer

import (
	"sort"

	"github.com/pion/rtcp"
)

const maxNackTimes = 10  // Max number of times a packet will be NACKed
const maxNackCache = 100 // Max NACK sn the sfu will keep reference

type nack struct {
	baseSN uint32
	nacked uint8
}

type nackQueue struct {
	nacks  []nack
	maxSN  uint16
	cycles uint32
}

func newNACKQueue() *nackQueue {
	return &nackQueue{
		nacks:  make([]nack, 0, maxNackCache+1),
		maxSN:  0,
		cycles: 0,
	}
}

func (n *nackQueue) remove(sn uint16) {
	var extSN uint32
	if sn > n.maxSN && sn&0x8000 == 1 && n.maxSN&0x8000 == 0 {
		extSN = (n.cycles - maxSN) | uint32(sn)
	} else {
		extSN = n.cycles | uint32(sn)
	}

	i := sort.Search(len(n.nacks), func(i int) bool { return n.nacks[i].baseSN >= extSN })
	if i > len(n.nacks) || n.nacks[i].baseSN != extSN {
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

	i := sort.Search(len(n.nacks), func(i int) bool { return n.nacks[i].baseSN >= extSN })
	if i < len(n.nacks) && n.nacks[i].baseSN == extSN {
		return
	}
	n.nacks = append(n.nacks, nack{})
	copy(n.nacks[i+1:], n.nacks[i:])
	n.nacks[i] = nack{
		baseSN: extSN,
		nacked: 0,
	}

	if len(n.nacks) > maxNackCache {
		n.nacks = n.nacks[1:]
	}
}

func (n *nackQueue) pairs() []rtcp.NackPair {
	i := 0
	var np rtcp.NackPair
	var nps []rtcp.NackPair
	for _, nck := range n.nacks {
		if nck.nacked >= maxNackTimes {
			continue
		}
		n.nacks[i] = nack{
			baseSN: nck.baseSN,
			nacked: nck.nacked + 1,
		}
		i++
		if np.PacketID == 0 || uint16(nck.baseSN) > np.PacketID+16 {
			if np.PacketID != 0 {
				nps = append(nps, np)
			}
			np.PacketID = uint16(nck.baseSN)
			np.LostPackets = 0
			continue
		}
		np.LostPackets |= 1 << (uint16(nck.baseSN) - np.PacketID - 1)
	}
	if np.PacketID != 0 {
		nps = append(nps, np)
	}
	n.nacks = n.nacks[:i]
	return nps
}
