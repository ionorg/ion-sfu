package buffer

import (
	"encoding/binary"
	"math"
)

const maxPktSize = 1460

type Bucket struct {
	buf []byte

	headSN   uint16
	step     int
	maxSteps int
}

func NewBucket(buf []byte) *Bucket {
	return &Bucket{
		buf:      buf,
		maxSteps: int(math.Floor(float64(len(buf))/float64(maxPktSize))) - 1,
	}
}

func (b *Bucket) addPacket(pkt []byte, sn uint16, latest bool) []byte {
	if !latest {
		return b.set(sn, pkt)
	}
	diff := sn - b.headSN
	b.headSN = sn
	for i := uint16(1); i < diff; i++ {
		b.step++
		if b.step >= b.maxSteps {
			b.step = 0
		}
	}
	return b.push(pkt)
}

func (b *Bucket) getPacket(buf []byte, sn uint16) (i int, err error) {
	p := b.get(sn)
	if p == nil {
		err = errPacketNotFound
		return
	}
	i = len(p)
	if cap(buf) < i {
		err = errBufferTooSmall
		return
	}
	if len(buf) < i {
		buf = buf[:i]
	}
	copy(buf, p)
	return
}

func (b *Bucket) push(pkt []byte) []byte {
	binary.BigEndian.PutUint16(b.buf[b.step*maxPktSize:], uint16(len(pkt)))
	off := b.step*maxPktSize + 2
	copy(b.buf[off:], pkt)
	b.step++
	if b.step >= b.maxSteps {
		b.step = 0
	}
	return b.buf[off : off+len(pkt)]
}

func (b *Bucket) get(sn uint16) []byte {
	pos := b.step - int(b.headSN-sn+1)
	if pos < 0 {
		if pos*-1 > b.maxSteps {
			return nil
		}
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

func (b *Bucket) set(sn uint16, pkt []byte) []byte {
	pos := b.step - int(b.headSN-sn+1)
	if pos < 0 {
		pos = b.maxSteps + pos + 1
	}
	off := pos * maxPktSize
	if off > len(b.buf) || off < 0 {
		return nil
	}
	binary.BigEndian.PutUint16(b.buf[off:], uint16(len(pkt)))
	copy(b.buf[off+2:], pkt)
	return b.buf[off+2 : off+2+len(pkt)]
}
