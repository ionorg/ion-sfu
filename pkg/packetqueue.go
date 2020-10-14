package sfu

import (
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

type queue struct {
	pkts     []*rtp.Packet
	head     int
	tail     int
	size     int
	headSN   uint16
	counter  int
	duration uint32
}

func (q *queue) nack() *rtcp.NackPair {
	for i := 0; i < 17; i++ {
		if q.get(q.counter-i-1) == nil {
			blp := uint16(0)
			for j := 1; j < q.counter-i; j++ {
				if q.get(q.counter-i-j-1) == nil {
					blp |= 1 << (j - 1)
				}
			}
			return &rtcp.NackPair{PacketID: q.headSN - uint16(q.counter-i-1), LostPackets: rtcp.PacketBitmap(blp)}
		}
	}
	return nil
}

func (q *queue) AddPacket(pkt *rtp.Packet, latest bool) {
	diff := pkt.SequenceNumber - q.headSN
	if !latest {
		q.set(q.head-int(diff), pkt)
		return
	}
	q.headSN = pkt.SequenceNumber
	for i := 1; i < int(diff); i++ {
		q.push(nil)
		q.counter++
	}
	q.counter++
	q.push(pkt)
	if q.counter >= 17 {
		q.nack()
		q.clean()
		q.counter -= 17
	}
}

func (q *queue) GetPacket(sn uint16) *rtp.Packet {
	diff := q.headSN - sn
	return q.get(int(diff))
}

func (q *queue) push(pkt *rtp.Packet) {
	q.resize()
	q.head = (q.head - 1) & (len(q.pkts) - 1)
	q.pkts[q.head] = pkt
	q.size++
}

func (q *queue) shift() {
	if q.size <= 0 {
		return
	}
	q.tail = (q.tail - 1) & (len(q.pkts) - 1)
	q.pkts[q.tail] = nil
	q.size--
}

func (q *queue) last() *rtp.Packet {
	return q.pkts[(q.tail-1)&(len(q.pkts)-1)]
}

func (q *queue) get(i int) *rtp.Packet {
	if i < 0 || i >= q.size {
		return nil
	}
	return q.pkts[(q.head+i)&(len(q.pkts)-1)]
}

func (q *queue) set(i int, pkt *rtp.Packet) {
	if i < 0 || i >= q.size {
		log.Warnf("warn: %v:", errPacketTooOld)
		return
	}
	q.pkts[(q.head+i)&(len(q.pkts)-1)] = pkt
}

func (q *queue) resize() {
	if len(q.pkts) == 0 {
		q.pkts = make([]*rtp.Packet, 128)
		return
	}
	if q.size == len(q.pkts) {
		newBuf := make([]*rtp.Packet, q.size<<1)
		if q.tail > q.head {
			copy(newBuf, q.pkts[q.head:q.tail])
		} else {
			n := copy(newBuf, q.pkts[q.head:])
			copy(newBuf[n:], q.pkts[:q.tail])
		}
		q.head = 0
		q.tail = q.size
		q.pkts = newBuf
	}
}

func (q *queue) clean() {
	last := q.last()
	for q.size > 120 && (last == nil || q.pkts[q.head].Timestamp-last.Timestamp > q.duration) {
		q.shift()
	}
}
