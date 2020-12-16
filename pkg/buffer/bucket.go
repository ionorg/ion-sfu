package buffer

import (
	"encoding/binary"
	"sync"

	"github.com/pion/rtcp"
)

const maxPktSize = 1460

type Bucket struct {
	sync.Mutex
	buf []byte

	ssrc     uint32
	headSN   uint16
	step     int
	maxSteps int

	duration uint32
	counter  int
	onLost   func(nack *rtcp.TransportLayerNack)
}

func NewBucket(size int) *Bucket {
	return &Bucket{
		buf:      make([]byte, size),
		maxSteps: size / maxPktSize,
	}
}

func (b *Bucket) addPacket(pkt []byte, sn uint16, latest bool) {
	b.Lock()
	defer b.Unlock()

	if !latest {
		b.set(sn, pkt)
		return
	}
	diff := sn - b.headSN
	b.headSN = sn
	for i := uint16(1); i < diff; i++ {
		b.step++
		if b.step > b.maxSteps {
			b.step = 0
		}
	}
	b.counter++
	b.push(pkt)
}

func (b *Bucket) getPacket(sn uint16) []byte {
	b.Lock()
	defer b.Unlock()
	p := b.get(sn)
	if p == nil {
		return nil
	}
	pkt := make([]byte, len(p))
	copy(pkt, p)
	return pkt
}

func (b *Bucket) push(pkt []byte) {
	binary.BigEndian.PutUint16(b.buf[b.step*maxPktSize:], uint16(len(pkt)))
	copy(b.buf[b.step*maxPktSize+2:], pkt)
	b.step++
	if b.step > b.maxSteps {
		b.step = 0
	}
}

func (b *Bucket) get(sn uint16) []byte {
	pos := b.step - int(b.headSN-sn+1)
	if pos < 0 {
		pos = b.maxSteps + pos + 1
	}
	off := pos * maxPktSize
	if off > len(b.buf) {
		return nil
	}
	if binary.BigEndian.Uint16(b.buf[off+4:off+6]) != sn {
		return nil
	}
	sz := int(binary.BigEndian.Uint16(b.buf[off : off+2]))
	return b.buf[off+2 : off+2+sz]
}

func (b *Bucket) set(sn uint16, pkt []byte) {
	pos := b.step - int(b.headSN-sn+1)
	if pos < 0 {
		pos = b.maxSteps + pos + 1
	}
	off := pos * maxPktSize
	if off > len(b.buf) {
		return
	}
	binary.BigEndian.PutUint16(b.buf[off:], uint16(len(pkt)))
	copy(b.buf[off+2:], pkt)
}
