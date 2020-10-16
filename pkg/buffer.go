package sfu

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/gammazero/deque"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

const (
	maxSN = 1 << 16
	// default buffer time by ms
	defaultBufferTime = 1000

	tccExtMapID      = 3
	deltaScaleFactor = 250
	baseScaleFactor  = deltaScaleFactor * (1 << 8)
	timeWrapPeriodUs = (int64(1) << 24) * baseScaleFactor
)

type rtpExtInfo struct {
	ExtTSN    uint32
	Timestamp int64
}

// Buffer contains all packets
type Buffer struct {
	mu sync.RWMutex

	pktQueue   queue
	codecType  webrtc.RTPCodecType
	simulcast  bool
	clockRate  uint32
	maxBitrate uint64

	lastSRNTPTime      uint64
	lastSRRTPTime      uint32
	lastSRRecv         int64 // Represents wall clock of the most recent sender report arrival
	baseSN             uint16
	cycles             uint32
	lastExpected       uint32
	lastReceived       uint32
	lostRate           float32
	ssrc               uint32
	lastPacketTime     int64  // Time the last RTP packet from this source was received
	lastRtcpPacketTime int64  // Time the last RTCP packet was received.
	lastRtcpSrTime     int64  // Time the last RTCP SR was received. Required for DLSR computation.
	packetCount        uint32 // Number of packets received from this source.
	lastTransit        uint32
	maxSeqNo           uint16 // The highest sequence number received in an RTP data packet
	jitter             uint32 // An estimate of the statistical variance of the RTP data packet inter-arrival time.
	totalByte          uint64

	remb        bool
	nack        bool
	transportCC bool

	tccExtInfo   []rtpExtInfo
	tccCycles    uint32
	tccLastExtSN uint32
	tccPktCtn    uint8
	tccLastSn    uint16
	lastExtInfo  uint16
}

// BufferOptions provides configuration options for the buffer
type BufferOptions struct {
	BufferTime int
	MaxBitRate uint64
}

// NewBuffer constructs a new Buffer
func NewBuffer(track *webrtc.Track, o BufferOptions) *Buffer {
	b := &Buffer{
		ssrc:       track.SSRC(),
		clockRate:  track.Codec().ClockRate,
		codecType:  track.Codec().Type,
		maxBitrate: o.MaxBitRate,
		simulcast:  len(track.RID()) > 0,
	}

	if o.BufferTime <= 0 {
		o.BufferTime = defaultBufferTime
	}
	b.pktQueue.duration = uint32(o.BufferTime) * b.clockRate / 1000
	b.pktQueue.ssrc = track.SSRC()
	log.Debugf("NewBuffer BufferOptions=%v", o)
	return b
}

// Push adds a RTP Packet, out of order, new packet may be arrived later
func (b *Buffer) Push(p *rtp.Packet) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.totalByte += uint64(p.MarshalSize())
	if b.packetCount == 0 {
		b.baseSN = p.SequenceNumber
		b.maxSeqNo = p.SequenceNumber
		b.pktQueue.headSN = p.SequenceNumber - 1
	} else if snDiff(b.maxSeqNo, p.SequenceNumber) <= 0 {
		if p.SequenceNumber < b.maxSeqNo {
			b.cycles += maxSN
		}
		b.maxSeqNo = p.SequenceNumber
	}
	b.packetCount++
	b.lastPacketTime = time.Now().UnixNano()
	arrival := uint32(b.lastPacketTime / 1e6 * int64(b.clockRate/1e3))
	transit := arrival - p.Timestamp
	if b.lastTransit != 0 {
		d := int32(transit - b.lastTransit)
		if d < 0 {
			d = -d
		}
		b.jitter += uint32(d) - ((b.jitter + 8) >> 4)
	}
	b.lastTransit = transit
	if b.codecType == webrtc.RTPCodecTypeVideo {
		b.pktQueue.AddPacket(p, p.SequenceNumber == b.maxSeqNo)
	}

	if b.transportCC {
		timestampUs := time.Now().UnixNano() / 1e3
		rtpTCC := rtp.TransportCCExtension{}
		if err := rtpTCC.Unmarshal(p.GetExtension(tccExtMapID)); err == nil {
			if rtpTCC.TransportSequence < 0x0fff && (b.tccLastSn&0xffff) > 0xf000 {
				b.tccCycles += maxSN
			}
			b.tccExtInfo = append(b.tccExtInfo, rtpExtInfo{
				ExtTSN:    b.tccCycles | uint32(rtpTCC.TransportSequence),
				Timestamp: timestampUs,
			})
		}
	}
}

func (b *Buffer) buildREMBPacket() *rtcp.ReceiverEstimatedMaximumBitrate {
	br := b.totalByte * 8
	if b.lostRate < 0.02 {
		br = uint64(float64(br)*1.08) + 1000
	}
	if b.lostRate > .1 {
		br = uint64(float64(br) * float64(1-0.5*b.lostRate))
	}
	if br > b.maxBitrate {
		br = b.maxBitrate
	}
	if br < 250000 {
		br = 250000
	}
	b.totalByte = 0
	return &rtcp.ReceiverEstimatedMaximumBitrate{
		SenderSSRC: b.ssrc,
		Bitrate:    br,
		SSRCs:      []uint32{b.ssrc},
	}
}

func (b *Buffer) buildTransportCCPacket() *rtcp.TransportLayerCC {
	sort.Slice(b.tccExtInfo, func(i, j int) bool {
		return b.tccExtInfo[i].ExtTSN < b.tccExtInfo[j].ExtTSN
	})
	tccPkts := make([]rtpExtInfo, 0, int(float64(len(b.tccExtInfo))*1.2))
	for i, tccExtInfo := range b.tccExtInfo {
		if tccExtInfo.ExtTSN < b.tccLastExtSN {
			b.tccExtInfo[i] = rtpExtInfo{}
			continue
		}
		if b.tccLastExtSN != 0 {
			for j := b.tccLastExtSN + 1; j < tccExtInfo.ExtTSN; j++ {
				tccPkts = append(tccPkts, rtpExtInfo{ExtTSN: j})
			}
		}
		b.tccLastExtSN = tccExtInfo.ExtTSN
	}
	b.tccExtInfo = b.tccExtInfo[:0]

	rtcpTCC := &rtcp.TransportLayerCC{
		Header: rtcp.Header{
			Padding: false,
			Count:   rtcp.FormatTCC,
			Type:    rtcp.TypeTransportSpecificFeedback,
			Length:  0,
		},
		MediaSSRC:          b.ssrc,
		BaseSequenceNumber: uint16(tccPkts[0].ExtTSN),
		PacketStatusCount:  uint16(len(tccPkts)),
		FbPktCount:         b.tccPktCtn,
	}
	b.tccPktCtn++

	firstRecv := false
	allSame := true
	timestamp := int64(0)
	lastStatus := rtcp.TypeTCCPacketReceivedWithoutDelta
	maxStatus := rtcp.TypeTCCPacketNotReceived

	var deltas deque.Deque
	var statusQ deque.Deque

	for _, stat := range tccPkts {
		status := rtcp.TypeTCCPacketNotReceived
		if stat.Timestamp != 0 {
			var delta int64
			if !firstRecv {
				firstRecv = true
				timestamp = stat.Timestamp
				rtcpTCC.ReferenceTime = uint32((stat.Timestamp / 64000) & 0x007FFFFF)
			}

			if stat.Timestamp > timestamp {
				delta = (stat.Timestamp - timestamp) / 250
			} else {
				delta = -(stat.Timestamp) / 250
			}

			if delta < 0 || delta > 255 {
				status = rtcp.TypeTCCPacketReceivedLargeDelta
			} else {
				status = rtcp.TypeTCCPacketReceivedSmallDelta
			}
			deltas.PushBack(delta)
			timestamp = stat.Timestamp
		}

		if allSame && lastStatus != rtcp.TypeTCCPacketReceivedWithoutDelta && status != lastStatus {
			if statusQ.Len() > 7 {
				rtcpTCC.PacketChunks = append(rtcpTCC.PacketChunks, &rtcp.RunLengthChunk{
					PacketStatusSymbol: lastStatus,
					RunLength:          uint16(statusQ.Len()),
				})
				statusQ.Clear()
				lastStatus = rtcp.TypeTCCPacketReceivedWithoutDelta
				maxStatus = rtcp.TypeTCCPacketNotReceived
				allSame = true
			} else {
				allSame = false
			}
		}
		statusQ.PushBack(status)
		if status > maxStatus {
			maxStatus = status
		}
		lastStatus = status

		if !allSame {
			if maxStatus == rtcp.TypeTCCPacketReceivedLargeDelta && statusQ.Len() > 6 {
				symbolList := make([]uint16, 7)
				for i := 0; i < 7; i++ {
					symbolList[i] = statusQ.PopFront().(uint16)
				}
				rtcpTCC.PacketChunks = append(rtcpTCC.PacketChunks, &rtcp.StatusVectorChunk{
					SymbolSize: rtcp.TypeTCCSymbolSizeTwoBit,
					SymbolList: symbolList,
				})
				lastStatus = rtcp.TypeTCCPacketReceivedWithoutDelta
				maxStatus = rtcp.TypeTCCPacketNotReceived
				allSame = true

				for i := 0; i < statusQ.Len(); i++ {
					status = statusQ.At(i).(uint16)
					if status > maxStatus {
						maxStatus = status
					}
					if allSame && lastStatus != rtcp.TypeTCCPacketReceivedWithoutDelta && status != lastStatus {
						allSame = false
					}
					lastStatus = status
				}
			} else if statusQ.Len() > 13 {
				symbolList := make([]uint16, 14)
				for i := 0; i < 14; i++ {
					symbolList[i] = statusQ.PopFront().(uint16)
				}
				rtcpTCC.PacketChunks = append(rtcpTCC.PacketChunks, &rtcp.StatusVectorChunk{
					SymbolSize: rtcp.TypeTCCSymbolSizeOneBit,
					SymbolList: symbolList,
				})
				lastStatus = rtcp.TypeTCCPacketReceivedWithoutDelta
				maxStatus = rtcp.TypeTCCPacketNotReceived
				allSame = true
			}
		}
	}

	if statusQ.Len() > 0 {
		if allSame {
			rtcpTCC.PacketChunks = append(rtcpTCC.PacketChunks, &rtcp.RunLengthChunk{
				PacketStatusSymbol: lastStatus,
				RunLength:          uint16(statusQ.Len()),
			})
		} else if maxStatus == rtcp.TypeTCCPacketReceivedLargeDelta {
			symbolList := make([]uint16, statusQ.Len())
			for i := 0; i < statusQ.Len(); i++ {
				symbolList[i] = statusQ.PopFront().(uint16)
			}
			rtcpTCC.PacketChunks = append(rtcpTCC.PacketChunks, &rtcp.StatusVectorChunk{
				SymbolSize: rtcp.TypeTCCSymbolSizeTwoBit,
				SymbolList: symbolList,
			})
		} else {
			symbolList := make([]uint16, statusQ.Len())
			for i := 0; i < statusQ.Len(); i++ {
				symbolList[i] = statusQ.PopFront().(uint16)
			}
			rtcpTCC.PacketChunks = append(rtcpTCC.PacketChunks, &rtcp.StatusVectorChunk{
				SymbolSize: rtcp.TypeTCCSymbolSizeOneBit,
				SymbolList: symbolList,
			})
		}
	}

	for deltas.Len() > 1 {
		delta := deltas.PopFront().(int64)
		if delta < 0 || delta > 255 {
			rDelta := int16(delta)
			if int64(rDelta) != delta {
				if rDelta > 0 {
					rDelta = math.MaxInt16
				} else {
					rDelta = math.MinInt16
				}
			}
			rtcpTCC.RecvDeltas = append(rtcpTCC.RecvDeltas, &rtcp.RecvDelta{
				Type:  rtcp.TypeTCCPacketReceivedLargeDelta,
				Delta: int64(rDelta),
			})
		} else {
			rtcpTCC.RecvDeltas = append(rtcpTCC.RecvDeltas, &rtcp.RecvDelta{
				Type:  rtcp.TypeTCCPacketReceivedSmallDelta,
				Delta: delta,
			})
		}
	}
	rtcpTCC.Header.Length = rtcpTCC.Len()/4 - 1
	return rtcpTCC
}

func (b *Buffer) buildReceptionReport() rtcp.ReceptionReport {
	extMaxSeq := b.cycles | uint32(b.maxSeqNo)
	expected := extMaxSeq - uint32(b.baseSN) + 1
	lost := expected - b.packetCount
	if b.packetCount == 0 {
		lost = 0
	}
	expectedInterval := expected - b.lastExpected
	b.lastExpected = expected

	receivedInterval := b.packetCount - b.lastReceived
	b.lastReceived = b.packetCount

	lostInterval := expectedInterval - receivedInterval

	b.lostRate = float32(lostInterval) / float32(expectedInterval)
	var fracLost uint8
	if expectedInterval != 0 && lostInterval > 0 {
		fracLost = uint8((lostInterval << 8) / expectedInterval)
	}
	var dlsr uint32
	if b.lastSRRecv != 0 {
		delayMS := uint32((time.Now().UnixNano() - b.lastSRRecv) / 1e6)
		dlsr = (delayMS / 1e3) << 16
		dlsr |= (delayMS % 1e3) * 65536 / 1000
	}

	rr := rtcp.ReceptionReport{
		SSRC:               b.ssrc,
		FractionLost:       fracLost,
		TotalLost:          lost,
		LastSequenceNumber: extMaxSeq,
		Jitter:             b.jitter >> 4,
		LastSenderReport:   uint32(b.lastSRNTPTime >> 16),
		Delay:              dlsr,
	}
	return rr
}

func (b *Buffer) setSenderReportData(rtpTime uint32, ntpTime uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastSRRTPTime = rtpTime
	b.lastSRNTPTime = ntpTime
	b.lastSRRecv = time.Now().UnixNano()
}

func (b *Buffer) getRTCP() []rtcp.Packet {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var pkts []rtcp.Packet

	RReport := &rtcp.ReceiverReport{
		Reports: []rtcp.ReceptionReport{b.buildReceptionReport()},
	}
	pkts = append(pkts, RReport)

	if b.codecType == webrtc.RTPCodecTypeVideo && !b.simulcast {
		pkts = append(pkts, b.buildREMBPacket())
	}

	return pkts
}

// WritePacket write buffer packet to requested track. and modify headers
func (b *Buffer) WritePacket(sn uint16, track *webrtc.Track, snOffset uint16, tsOffset, ssrc uint32) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if bufferPkt := b.pktQueue.GetPacket(sn); bufferPkt != nil {
		bSsrc := bufferPkt.SSRC
		bufferPkt.SequenceNumber -= snOffset
		bufferPkt.Timestamp -= tsOffset
		bufferPkt.SSRC = ssrc
		err := track.WriteRTP(bufferPkt)
		bufferPkt.Timestamp += tsOffset
		bufferPkt.SequenceNumber += snOffset
		bufferPkt.SSRC = bSsrc
		return err
	}
	return errPacketNotFound
}

func (b *Buffer) onLostHandler(fn func(nack *rtcp.TransportLayerNack)) {
	b.pktQueue.onLost = fn
}
