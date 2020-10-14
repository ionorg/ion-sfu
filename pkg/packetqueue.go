package sfu

import (
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtp"
)

type queue struct {
	buf      []*rtp.Packet
	head     int
	tail     int
	count    int
	headSN   uint16
	duration uint32
	missing  map[uint16]uint32
}

func (q *queue) AddPacket(pkt *rtp.Packet, latest bool) {
	diff := pkt.SequenceNumber - q.headSN
	if !latest {
		q.set(q.head-int(diff), pkt)
		return
	}
	q.headSN = pkt.SequenceNumber
	for i := 0; i < int(diff); i++ {
		// Missing packets
		q.push(nil)
	}
	q.push(pkt)
	q.clean()
}

func (q *queue) GetPacket(sn uint16) *rtp.Packet {
	diff := q.headSN - sn
	return q.get(int(diff))
}

func (q *queue) push(pkt *rtp.Packet) {
	q.resize()
	q.head = (q.head - 1) & (len(q.buf) - 1)
	q.buf[q.head] = pkt
	q.count++
}

func (q *queue) shift() {
	if q.count <= 0 {
		return
	}
	q.tail = (q.tail - 1) & (len(q.buf) - 1)
	q.buf[q.tail] = nil
	q.count--
}

func (q *queue) get(i int) *rtp.Packet {
	if i < 0 || i >= q.count {
		return nil
	}
	return q.buf[(q.head+i)&(len(q.buf)-1)]
}

func (q *queue) set(i int, pkt *rtp.Packet) {
	if i < 0 || i >= q.count {
		log.Warnf("warn: %v:", errPacketTooOld)
		return
	}
	q.buf[(q.head+i)&(len(q.buf)-1)] = pkt
}

func (q *queue) resize() {
	if len(q.buf) == 0 {
		q.buf = make([]*rtp.Packet, 1024)
		q.missing = make(map[uint16]uint32)
		return
	}
	if q.count == len(q.buf) {
		newBuf := make([]*rtp.Packet, q.count<<1)
		if q.tail > q.head {
			copy(newBuf, q.buf[q.head:q.tail])
		} else {
			n := copy(newBuf, q.buf[q.head:])
			copy(newBuf[n:], q.buf[:q.tail])
		}
		q.head = 0
		q.tail = q.count
		q.buf = newBuf
	}
}

func (q *queue) clean() {
	for q.buf[q.head].Timestamp-q.buf[q.tail].Timestamp > q.duration {
		q.shift()
	}
}
