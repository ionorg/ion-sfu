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

	tccReportDelta = 1e8
)

type rtpExtInfo struct {
	ExtTSN    uint32
	Timestamp int64
}

type TransportWideCC struct {
	sync.Mutex

	tccExtInfo    []rtpExtInfo
	tccLastReport int64
	tccExt        uint8
	tccCycles     uint32
	tccLastExtSN  uint32
	tccPktCtn     uint8
	tccLastSn     uint16
	lastExtInfo   uint16

	sSSRC uint32

	len      uint16
	deltaLen uint16
	payload  []byte
	deltas   []byte
}

func newTransportWideCC() *TransportWideCC {
	return &TransportWideCC{
		tccExtInfo: make([]rtpExtInfo, 0, 101),
		payload:    make([]byte, 50),
		deltas:     make([]byte, 100),
	}
}

func (t *TransportWideCC) push(sn uint16, timeNS int64) {
	t.Lock()
	defer t.Unlock()

	if sn < 0x0fff && (t.tccLastSn&0xffff) > 0xf000 {
		t.tccCycles += maxSN
	}
	t.tccExtInfo = append(t.tccExtInfo, rtpExtInfo{
		ExtTSN:    t.tccCycles | uint32(sn),
		Timestamp: timeNS / 1e3,
	})

	if len(t.tccExtInfo) >= 100 || timeNS-t.tccLastReport >= tccReportDelta {

	}
}

func (t *TransportWideCC) buildTransportCCPacket() (*rtcp.RawPacket, error) {
	if len(t.tccExtInfo) == 0 {
		return nil, nil
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
	allSame := true
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
				timestamp = stat.Timestamp
				t.writeHeader(
					uint16(tccPkts[0].ExtTSN),
					uint16(len(tccPkts)),
					uint32((stat.Timestamp/64e3)&0x007FFFFF),
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
				t.writeDelta(uint16(rDelta), status)
			} else {
				status = rtcp.TypeTCCPacketReceivedSmallDelta
				t.writeDelta(uint16(delta), status)
			}
			timestamp = stat.Timestamp
		}

		if allSame && status != lastStatus {
			if statusList.Len() > 7 && lastStatus != rtcp.TypeTCCPacketReceivedWithoutDelta {
				if err := t.writeRunLengthChunk(lastStatus, uint16(statusList.Len())); err != nil {
					return nil, err
				}
				statusList.Clear()
				lastStatus = rtcp.TypeTCCPacketReceivedWithoutDelta
				maxStatus = rtcp.TypeTCCPacketNotReceived
				allSame = true
			} else {
				allSame = false
			}
		}
		statusList.PushBack(status)
		if status > maxStatus {
			maxStatus = status
		}
		lastStatus = status

		if !allSame && maxStatus == rtcp.TypeTCCPacketReceivedLargeDelta && statusList.Len() > 6 {
			symbolList := make([]uint16, 7)
			for i := 0; i < 7; i++ {
				symbolList[i] = statusList.PopFront().(uint16)
			}
			if err := t.writeStatusSymbolChunk(rtcp.TypeTCCSymbolSizeTwoBit, symbolList); err != nil {
				return nil, err
			}
			lastStatus = rtcp.TypeTCCPacketReceivedWithoutDelta
			maxStatus = rtcp.TypeTCCPacketNotReceived
			allSame = true

			for i := 0; i < statusList.Len(); i++ {
				status = statusList.At(i).(uint16)
				if status > maxStatus {
					maxStatus = status
				}
				if allSame && lastStatus != rtcp.TypeTCCPacketReceivedWithoutDelta && status != lastStatus {
					allSame = false
				}
				lastStatus = status
			}
		} else if !allSame && statusList.Len() > 13 {
			symbolList := make([]uint16, 14)
			for i := 0; i < 14; i++ {
				symbolList[i] = statusList.PopFront().(uint16)
			}
			if err := t.writeStatusSymbolChunk(rtcp.TypeTCCSymbolSizeOneBit, symbolList); err != nil {
				return nil, err
			}
			lastStatus = rtcp.TypeTCCPacketReceivedWithoutDelta
			maxStatus = rtcp.TypeTCCPacketNotReceived
			allSame = true
		}

	}

	if statusList.Len() > 0 {
		if allSame && lastStatus != rtcp.TypeTCCPacketReceivedWithoutDelta {
			if err := t.writeRunLengthChunk(lastStatus, uint16(statusList.Len())); err != nil {
				return nil, err
			}
		} else if maxStatus > rtcp.TypeTCCPacketReceivedSmallDelta {
			symbolList := make([]uint16, statusList.Len())
			for i := 0; i < statusList.Len(); i++ {
				symbolList[i] = statusList.PopFront().(uint16)
			}
			if err := t.writeStatusSymbolChunk(rtcp.TypeTCCSymbolSizeTwoBit, symbolList); err != nil {
				return nil, err
			}
		} else {
			symbolList := make([]uint16, statusList.Len())
			for i := 0; i < statusList.Len(); i++ {
				symbolList[i] = statusList.PopFront().(uint16)
			}
			if err := t.writeStatusSymbolChunk(rtcp.TypeTCCSymbolSizeOneBit, symbolList); err != nil {
				return nil, err
			}
		}
	}

	pLen := t.len + t.deltaLen
	pad := pLen%4 != 0
	for pLen%4 != 0 {
		pLen++
	}
	hdr := rtcp.Header{
		Padding: pad,
		Length:  (pLen / 4) - 1,
		Count:   rtcp.FormatTCC,
		Type:    rtcp.TypeTransportSpecificFeedback,
	}
	hb, _ := hdr.Marshal()
	pkt := make(rtcp.RawPacket, pLen+4)
	copy(pkt, hb)
	copy(pkt[4:], t.payload[:t.len])
	copy(pkt[4+t.len:], t.deltas[:t.deltaLen])
	t.deltaLen = 0
	return &pkt, nil
}

func (t *TransportWideCC) writeHeader(bSN, packetCount uint16, refTime uint32) {
	binary.BigEndian.PutUint32(t.payload, t.sSSRC)
	binary.BigEndian.PutUint32(t.payload[4:], rand.Uint32())
	binary.BigEndian.PutUint16(t.payload[baseSequenceNumberOffset:], bSN)
	binary.BigEndian.PutUint16(t.payload[packetStatusCountOffset:], packetCount)
	ReferenceTimeAndFbPktCount := appendNBitsToUint32(0, 24, refTime)
	ReferenceTimeAndFbPktCount = appendNBitsToUint32(ReferenceTimeAndFbPktCount, 8, uint32(t.tccPktCtn))
	binary.BigEndian.PutUint32(t.payload[referenceTimeOffset:], ReferenceTimeAndFbPktCount)
	t.len = 20
}

func (t *TransportWideCC) writeRunLengthChunk(symbol uint16, runLength uint16) error {
	dst, err := setNBitsOfUint16(0, 1, 0, 0)
	if err != nil {
		return err
	}
	dst, err = setNBitsOfUint16(dst, 2, 1, symbol)
	if err != nil {
		return err
	}
	dst, err = setNBitsOfUint16(dst, 13, 3, runLength)
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint16(t.payload[t.len:], dst)
	t.len += 2
	return nil
}

func (t *TransportWideCC) writeStatusSymbolChunk(symbolSize uint16, symbolList []uint16) error {
	dst, err := setNBitsOfUint16(0, 1, 0, 1)
	if err != nil {
		return err
	}
	dst, err = setNBitsOfUint16(dst, 1, 1, symbolSize)
	if err != nil {
		return err
	}

	numOfBits := symbolSize + 1
	// append 14 bit SymbolList
	for i, s := range symbolList {
		index := numOfBits*uint16(i) + 2
		dst, err = setNBitsOfUint16(dst, numOfBits, index, s)
		if err != nil {
			return err
		}
	}
	binary.BigEndian.PutUint16(t.payload[t.len:], dst)
	t.len += 2
	return nil
}

func (t *TransportWideCC) writeDelta(deltaType, delta uint16) {
	if deltaType == rtcp.TypeTCCPacketReceivedSmallDelta {
		copy(t.deltas[t.deltaLen:], []byte{byte(delta)})
		t.deltaLen++
		return
	}
	t.deltaLen += 2
	binary.BigEndian.PutUint16(t.deltas[t.deltaLen:], delta)
}
