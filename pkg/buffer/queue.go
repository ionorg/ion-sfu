package buffer

import (
	"sync"

	log "github.com/pion/ion-log"
)

const maxDisorder = 30

type PacketQueue struct {
	pkts     [][]byte
	pool     *sync.Pool
	head     int
	tail     int
	size     int
	maxSize  int
	headSN   uint16
	duration uint32
}

func NewPacketQueue(pool *sync.Pool, size int) *PacketQueue {
	return &PacketQueue{
		pool:    pool,
		maxSize: size,
	}
}

func (p *PacketQueue) AddPacket(packet []byte, sn uint16, latest bool) []byte {
	pkt := p.pool.Get().([]byte)
	pkt = pkt[:len(packet)]
	copy(pkt, packet)

	if !latest {
		p.set(int(p.headSN-sn), pkt)
		return pkt
	}
	diff := sn - p.headSN
	p.headSN = sn
	for i := uint16(1); i < diff; i++ {
		p.push(nil)
	}
	p.push(pkt)

	if p.size > p.maxSize {
		p.shift()
	}
	return pkt
}

func (p *PacketQueue) GetPacket(buf []byte, sn uint16) (i int, err error) {
	pkt := p.get(int(p.headSN - sn))
	if pkt == nil {
		err = errPacketNotFound
		return
	}
	i = len(pkt)
	if cap(buf) < i {
		err = errBufferTooSmall
		return
	}
	if len(buf) < i {
		buf = buf[:i]
	}
	copy(buf, pkt)
	return
}

func (p *PacketQueue) Close() {
	for p.size > 0 {
		p.shift()
	}
}

func (p *PacketQueue) push(pkt []byte) {
	p.resize()
	p.head = (p.head - 1) & (len(p.pkts) - 1)
	p.pkts[p.head] = pkt
	p.size++
}

func (p *PacketQueue) prepend(pkt []byte) {
	p.pkts[p.tail] = pkt
	p.tail = (p.tail + 1) & (len(p.pkts) - 1)
	p.size++
}

func (p *PacketQueue) shift() {
	if p.size <= 0 {
		return
	}
	p.tail = (p.tail - 1) & (len(p.pkts) - 1)
	if p.pkts[p.tail] != nil {
		p.pool.Put(p.pkts[p.tail])
		p.pkts[p.tail] = nil
	}
	p.size--
}

func (p *PacketQueue) last() []byte {
	return p.pkts[(p.tail-1)&(len(p.pkts)-1)]
}

func (p *PacketQueue) get(i int) []byte {
	if i < 0 || i >= p.size {
		return nil
	}
	return p.pkts[(p.head+i)&(len(p.pkts)-1)]
}

func (p *PacketQueue) set(i int, pkt []byte) {
	if i < 0 || i > maxDisorder {
		log.Warnf("packet discarded (disorder), packet sn: %d, head sn: %d", p.headSN-uint16(i), p.headSN)
		return
	}

	if i >= p.size && i < maxDisorder {
		for j := 1; j < i; j++ {
			p.prepend(nil)
		}
		p.prepend(pkt)
		return
	}

	p.pkts[(p.head+i)&(len(p.pkts)-1)] = pkt
}

func (p *PacketQueue) resize() {
	if len(p.pkts) == 0 {
		p.pkts = make([][]byte, 1<<7)
		return
	}
	if p.size == len(p.pkts) {
		newBuf := make([][]byte, p.size<<1)
		if p.tail > p.head {
			copy(newBuf, p.pkts[p.head:p.tail])
		} else {
			n := copy(newBuf, p.pkts[p.head:])
			copy(newBuf[n:], p.pkts[:p.tail])
		}
		p.head = 0
		p.tail = p.size
		p.pkts = newBuf
	}
}
