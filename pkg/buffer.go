package sfu

import (
	"sync"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

const (
	maxSN = 1 << 16
	// default buffer time by ms
	defaultBufferTime = 1000
)

func tsDelta(x, y uint32) uint32 {
	if x > y {
		return x - y
	}
	return y - x
}

type rtpExtInfo struct {
	// transport sequence num
	TSN       uint16
	Timestamp int64
}

// Buffer contains all packets
type Buffer struct {
	pktQueue   queue
	codecType  webrtc.RTPCodecType
	clockRate  uint32
	maxBitrate uint64

	lastSRNTPTime        uint64
	lastSRRTPTime        uint32
	lastSRRecv           int64 // Represents wall clock of the most recent sender report arrival
	baseSN               uint16
	cycles               uint32
	lastExpected         uint32
	lastReceived         uint32
	lostRate             float32
	ssrc                 uint32
	lastPacketTime       int64  // Time the last RTP packet from this source was received
	lastRtcpPacketTime   int64  // Time the last RTCP packet was received.
	lastRtcpSrTime       int64  // Time the last RTCP SR was received. Required for DLSR computation.
	packetCount          uint32 // Number of packets received from this source.
	lastTransit          uint32
	cumulativePacketLost uint32 // The total of RTP packets that have been lost since the beginning of reception.
	maxSeqNo             uint16 // The highest sequence number received in an RTP data packet
	jitter               uint32 // An estimate of the statistical variance of the RTP data packet inter-arrival time.
	totalByte            uint64

	mu sync.RWMutex
}

// BufferOptions provides configuration options for the buffer
type BufferOptions struct {
	BufferTime int
	MaxBitRate uint64
}

// NewBuffer constructs a new Buffer
func NewBuffer(track *webrtc.Track, o BufferOptions) *Buffer {
	b := &Buffer{
		ssrc:      track.SSRC(),
		clockRate: track.Codec().ClockRate,
		codecType: track.Codec().Type,
	}

	if o.BufferTime <= 0 {
		o.BufferTime = defaultBufferTime
	}
	b.pktQueue.duration = uint32(o.BufferTime) * b.clockRate / 1000
	println(b.pktQueue.duration)
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
	} else {
		if snDiff(b.maxSeqNo, p.SequenceNumber) <= 0 {
			if p.SequenceNumber < b.maxSeqNo {
				b.cycles += maxSN
			}
			b.maxSeqNo = p.SequenceNumber
		}
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
	RReport := &rtcp.ReceiverReport{
		Reports: []rtcp.ReceptionReport{b.buildReceptionReport()},
	}
	remb := b.buildREMBPacket()
	return []rtcp.Packet{RReport, remb}
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
