package sfu

import (
	"encoding/binary"
	"math"
	"math/rand"
	"sort"
	"sync"

	"github.com/gammazero/deque"
	"github.com/pion/rtcp"
)

const (
	baseSequenceNumberOffset = 8
	packetStatusCountOffset  = 10
	referenceTimeOffset      = 12

	tccReportDelta          = 1e8
	tccReportDeltaAfterMark = 50e6
)

type rtpExtInfo struct {
	ExtTSN    uint32
	Timestamp int64
}

type TransportWideCC struct {
	sync.Mutex
	rtcpCh chan []rtcp.Packet

	tccExtInfo    []rtpExtInfo
	tccLastReport int64
	tccCycles     uint32
	tccLastExtSN  uint32
	tccPktCtn     uint8
	tccLastSn     uint16
	lastExtInfo   uint16
	mSSRC         uint32
	sSSRC         uint32

	len      uint16
	deltaLen uint16
	payload  [100]byte
	deltas   [200]byte
	chunk    uint16
}

func newTransportWideCC(ch chan []rtcp.Packet) *TransportWideCC {
	return &TransportWideCC{
		tccExtInfo: make([]rtpExtInfo, 0, 101),
		rtcpCh:     ch,
		sSSRC:      rand.Uint32(),
	}
}

func (t *TransportWideCC) push(sn uint16, timeNS int64, marker bool) {
	t.Lock()
	defer t.Unlock()

	if sn < 0x0fff && (t.tccLastSn&0xffff) > 0xf000 {
		t.tccCycles += maxSN
	}
	t.tccExtInfo = append(t.tccExtInfo, rtpExtInfo{
		ExtTSN:    t.tccCycles | uint32(sn),
		Timestamp: timeNS / 1e3,
	})
	t.tccLastSn = sn
	delta := timeNS - t.tccLastReport
	if delta >= tccReportDelta || len(t.tccExtInfo) > 100 || (marker && delta >= tccReportDeltaAfterMark) {
		if pkt := t.buildTransportCCPacket(); pkt != nil {
			t.rtcpCh <- []rtcp.Packet{pkt}
		}
		t.tccLastReport = timeNS
	}
}

func (t *TransportWideCC) buildTransportCCPacket() *rtcp.RawPacket {
	if len(t.tccExtInfo) == 0 {
		return nil
	}
	sort.Slice(t.tccExtInfo, func(i, j int) bool {
		return t.tccExtInfo[i].ExtTSN < t.tccExtInfo[j].ExtTSN
	})
	tccPkts := make([]rtpExtInfo, 0, int(float64(len(t.tccExtInfo))*1.2))
	for _, tccExtInfo := range t.tccExtInfo {
		if tccExtInfo.ExtTSN < t.tccLastExtSN {
			continue
		}
		if t.tccLastExtSN != 0 {
			for j := t.tccLastExtSN + 1; j < tccExtInfo.ExtTSN; j++ {
				tccPkts = append(tccPkts, rtpExtInfo{ExtTSN: j})
			}
		}
		t.tccLastExtSN = tccExtInfo.ExtTSN
		tccPkts = append(tccPkts, tccExtInfo)
	}
	t.tccExtInfo = t.tccExtInfo[:0]

	firstRecv := false
	same := true
	timestamp := int64(0)
	lastStatus := rtcp.TypeTCCPacketReceivedWithoutDelta
	maxStatus := rtcp.TypeTCCPacketNotReceived

	var statusList deque.Deque
	statusList.SetMinCapacity(3)

	for _, stat := range tccPkts {
		status := rtcp.TypeTCCPacketNotReceived
		if stat.Timestamp != 0 {
			var delta int64
			if !firstRecv {
				firstRecv = true
				refTime := stat.Timestamp / 64e3
				timestamp = refTime * 64e3
				t.writeHeader(
					uint16(tccPkts[0].ExtTSN),
					uint16(len(tccPkts)),
					uint32(refTime),
				)
				t.tccPktCtn++
			}

			delta = (stat.Timestamp - timestamp) / 250
			if delta < 0 || delta > 255 {
				status = rtcp.TypeTCCPacketReceivedLargeDelta
				rDelta := int16(delta)
				if int64(rDelta) != delta {
					if rDelta > 0 {
						rDelta = math.MaxInt16
					} else {
						rDelta = math.MinInt16
					}
				}
				t.writeDelta(status, uint16(rDelta))
			} else {
				status = rtcp.TypeTCCPacketReceivedSmallDelta
				t.writeDelta(status, uint16(delta))
			}
			timestamp = stat.Timestamp
		}

		if same && status != lastStatus && lastStatus != rtcp.TypeTCCPacketReceivedWithoutDelta {
			if statusList.Len() > 7 {
				t.writeRunLengthChunk(lastStatus, uint16(statusList.Len()))
				statusList.Clear()
				lastStatus = rtcp.TypeTCCPacketReceivedWithoutDelta
				maxStatus = rtcp.TypeTCCPacketNotReceived
				same = true
			} else {
				same = false
			}
		}
		statusList.PushBack(status)
		if status > maxStatus {
			maxStatus = status
		}
		lastStatus = status

		if !same && maxStatus == rtcp.TypeTCCPacketReceivedLargeDelta && statusList.Len() > 6 {
			for i := 0; i < 7; i++ {
				t.createStatusSymbolChunk(rtcp.TypeTCCSymbolSizeTwoBit, statusList.PopFront().(uint16), i)
			}
			t.writeStatusSymbolChunk(rtcp.TypeTCCSymbolSizeTwoBit)
			lastStatus = rtcp.TypeTCCPacketReceivedWithoutDelta
			maxStatus = rtcp.TypeTCCPacketNotReceived
			same = true

			for i := 0; i < statusList.Len(); i++ {
				status = statusList.At(i).(uint16)
				if status > maxStatus {
					maxStatus = status
				}
				if same && lastStatus != rtcp.TypeTCCPacketReceivedWithoutDelta && status != lastStatus {
					same = false
				}
				lastStatus = status
			}
		} else if !same && statusList.Len() > 13 {
			for i := 0; i < 14; i++ {
				t.createStatusSymbolChunk(rtcp.TypeTCCSymbolSizeOneBit, statusList.PopFront().(uint16), i)
			}
			t.writeStatusSymbolChunk(rtcp.TypeTCCSymbolSizeOneBit)
			lastStatus = rtcp.TypeTCCPacketReceivedWithoutDelta
			maxStatus = rtcp.TypeTCCPacketNotReceived
			same = true
		}
	}

	if statusList.Len() > 0 {
		if same {
			t.writeRunLengthChunk(lastStatus, uint16(statusList.Len()))
		} else if maxStatus == rtcp.TypeTCCPacketReceivedLargeDelta {
			for i := 0; i < statusList.Len(); i++ {
				t.createStatusSymbolChunk(rtcp.TypeTCCSymbolSizeTwoBit, statusList.PopFront().(uint16), i)
			}
			t.writeStatusSymbolChunk(rtcp.TypeTCCSymbolSizeTwoBit)
		} else {
			for i := 0; i < statusList.Len(); i++ {
				t.createStatusSymbolChunk(rtcp.TypeTCCSymbolSizeOneBit, statusList.PopFront().(uint16), i)
			}
			t.writeStatusSymbolChunk(rtcp.TypeTCCSymbolSizeOneBit)
		}
	}

	pLen := t.len + t.deltaLen + 4
	pad := pLen%4 != 0
	var padSize uint8
	for pLen%4 != 0 {
		padSize++
		pLen++
	}
	hdr := rtcp.Header{
		Padding: pad,
		Length:  (pLen / 4) - 1,
		Count:   rtcp.FormatTCC,
		Type:    rtcp.TypeTransportSpecificFeedback,
	}
	hb, _ := hdr.Marshal()
	pkt := make(rtcp.RawPacket, pLen)
	copy(pkt, hb)
	copy(pkt[4:], t.payload[:t.len])
	copy(pkt[4+t.len:], t.deltas[:t.deltaLen])
	if pad {
		pkt[len(pkt)-1] = padSize
	}
	t.deltaLen = 0
	return &pkt
}

func (t *TransportWideCC) writeHeader(bSN, packetCount uint16, refTime uint32) {
	/*
	   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	   |                     SSRC of packet sender                     |
	   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	   |                      SSRC of media source                     |
	   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	   |      base sequence number     |      packet status count      |
	   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	   |                 reference time                | fb pkt. count |
	   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	*/
	binary.BigEndian.PutUint32(t.payload[0:], t.sSSRC)
	binary.BigEndian.PutUint32(t.payload[4:], t.mSSRC)
	binary.BigEndian.PutUint16(t.payload[baseSequenceNumberOffset:], bSN)
	binary.BigEndian.PutUint16(t.payload[packetStatusCountOffset:], packetCount)
	binary.BigEndian.PutUint32(t.payload[referenceTimeOffset:], refTime<<8|uint32(t.tccPktCtn))
	t.len = 16
}

func (t *TransportWideCC) writeRunLengthChunk(symbol uint16, runLength uint16) {
	/*
	   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	   |T| S |       Run Length        |
	   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	*/
	binary.BigEndian.PutUint16(t.payload[t.len:], symbol<<13|runLength)
	t.len += 2
}

func (t *TransportWideCC) createStatusSymbolChunk(symbolSize, symbol uint16, i int) {
	/*
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		|T|S|       symbol list         |
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	*/
	numOfBits := symbolSize + 1
	t.chunk = setNBitsOfUint16(t.chunk, numOfBits, numOfBits*uint16(i)+2, symbol)
}

func (t *TransportWideCC) writeStatusSymbolChunk(symbolSize uint16) {
	t.chunk = setNBitsOfUint16(t.chunk, 1, 0, 1)
	t.chunk = setNBitsOfUint16(t.chunk, 1, 1, symbolSize)
	binary.BigEndian.PutUint16(t.payload[t.len:], t.chunk)
	t.chunk = 0
	t.len += 2
}

func (t *TransportWideCC) writeDelta(deltaType, delta uint16) {
	if deltaType == rtcp.TypeTCCPacketReceivedSmallDelta {
		t.deltas[t.deltaLen] = byte(delta)
		t.deltaLen++
		return
	}
	binary.BigEndian.PutUint16(t.deltas[t.deltaLen:], delta)
	t.deltaLen += 2
}
