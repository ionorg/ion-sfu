package sfu

import (
	"sync/atomic"
	"time"

	log "github.com/pion/ion-log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

const (
	maxSN = 1 << 16
	// default buffer time by ms
	defaultBufferTime = 1000

	reportDelta = 1e9
)

// Buffer contains all packets
type Buffer struct {
	pktQueue   queue
	codecType  webrtc.RTPCodecType
	simulcast  bool
	mediaSSRC  uint32
	clockRate  uint32
	maxBitrate uint64
	lastReport int64
	twccExt    uint8

	// supported feedbacks
	remb bool
	nack bool
	tcc  bool

	lastSRNTPTime      uint64
	lastSRRTPTime      uint32
	lastSRRecv         int64 // Represents wall clock of the most recent sender report arrival
	baseSN             uint16
	cycles             uint32
	lastExpected       uint32
	lastReceived       uint32
	lostRate           float32
	lastPacketTime     int64  // Time the last RTP packet from this source was received
	lastRtcpPacketTime int64  // Time the last RTCP packet was received.
	lastRtcpSrTime     int64  // Time the last RTCP SR was received. Required for DLSR computation.
	packetCount        uint32 // Number of packets received from this source.
	lastTransit        uint32
	maxSeqNo           uint16  // The highest sequence number received in an RTP data packet
	jitter             float64 // An estimate of the statistical variance of the RTP data packet inter-arrival time.
	totalByte          uint64
	// callbacks
	feedbackTWCC func(sn uint16, timeNS int64, marker bool)
	feedbackCB   func([]rtcp.Packet)
}

// BufferOptions provides configuration options for the buffer
type BufferOptions struct {
	TWCCExt    int
	BufferTime int
	MaxBitRate uint64
}

// NewBuffer constructs a new Buffer
func NewBuffer(track *webrtc.Track, o BufferOptions) *Buffer {
	b := &Buffer{
		mediaSSRC:  track.SSRC(),
		clockRate:  track.Codec().ClockRate,
		codecType:  track.Codec().Type,
		maxBitrate: o.MaxBitRate,
		simulcast:  len(track.RID()) > 0,
		twccExt:    uint8(o.TWCCExt),
	}
	if o.BufferTime <= 0 {
		o.BufferTime = defaultBufferTime
	}
	b.pktQueue.duration = uint32(o.BufferTime) * b.clockRate / 1000
	b.pktQueue.ssrc = track.SSRC()

	for _, fb := range track.Codec().RTCPFeedback {
		switch fb.Type {
		case webrtc.TypeRTCPFBGoogREMB:
			log.Debugf("Setting feedback %s", webrtc.TypeRTCPFBGoogREMB)
			b.remb = true
		case webrtc.TypeRTCPFBTransportCC:
			log.Debugf("Setting feedback %s", webrtc.TypeRTCPFBTransportCC)
			b.tcc = true
		case webrtc.TypeRTCPFBNACK:
			log.Debugf("Setting feedback %s", webrtc.TypeRTCPFBNACK)
			b.nack = true
		}
	}
	log.Debugf("NewBuffer BufferOptions=%v", o)
	return b
}

// push adds a RTP Packet, out of order, new packet may be arrived later
func (b *Buffer) push(p *rtp.Packet) {
	b.lastPacketTime = time.Now().UnixNano()
	b.totalByte += uint64(p.MarshalSize())
	if b.packetCount == 0 {
		b.baseSN = p.SequenceNumber
		b.maxSeqNo = p.SequenceNumber
		b.pktQueue.headSN = p.SequenceNumber - 1
		b.lastReport = b.lastPacketTime
	} else if snDiff(b.maxSeqNo, p.SequenceNumber) <= 0 {
		if p.SequenceNumber < b.maxSeqNo {
			b.cycles += maxSN
		}
		b.maxSeqNo = p.SequenceNumber
	}
	b.packetCount++
	arrival := uint32(b.lastPacketTime / 1e6 * int64(b.clockRate/1e3))
	transit := arrival - p.Timestamp
	if b.lastTransit != 0 {
		d := int32(transit - b.lastTransit)
		if d < 0 {
			d = -d
		}
		b.jitter += (float64(d) - b.jitter) / 16
	}
	b.lastTransit = transit
	if b.codecType == webrtc.RTPCodecTypeVideo {
		b.pktQueue.AddPacket(p, p.SequenceNumber == b.maxSeqNo)
	}

	if b.tcc {
		rtpTCC := rtp.TransportCCExtension{}
		if err := rtpTCC.Unmarshal(p.GetExtension(b.twccExt)); err == nil {
			b.feedbackTWCC(rtpTCC.TransportSequence, b.lastPacketTime, p.Marker)
		}
	}

	if b.lastPacketTime-b.lastReport >= reportDelta {
		b.feedbackCB(b.getRTCP())
		b.lastReport = b.lastPacketTime
	}

}

func (b *Buffer) buildREMBPacket() *rtcp.ReceiverEstimatedMaximumBitrate {
	br := b.totalByte * 8
	if b.lostRate < 0.02 {
		br = uint64(float64(br)*1.09) + 2000
	}
	if b.lostRate > .1 {
		br = uint64(float64(br) * float64(1-0.5*b.lostRate))
	}
	if br > b.maxBitrate {
		br = b.maxBitrate
	}
	if br < 100000 {
		br = 100000
	}
	b.totalByte = 0

	return &rtcp.ReceiverEstimatedMaximumBitrate{
		Bitrate: br,
		SSRCs:   []uint32{b.mediaSSRC},
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
	lastSRRecv := atomic.LoadInt64(&b.lastSRRecv)
	lastSRNTPTime := atomic.LoadUint64(&b.lastSRNTPTime)

	if lastSRRecv != 0 {
		delayMS := uint32((time.Now().UnixNano() - lastSRRecv) / 1e6)
		dlsr = (delayMS / 1e3) << 16
		dlsr |= (delayMS % 1e3) * 65536 / 1000
	}

	rr := rtcp.ReceptionReport{
		SSRC:               b.mediaSSRC,
		FractionLost:       fracLost,
		TotalLost:          lost,
		LastSequenceNumber: extMaxSeq,
		Jitter:             uint32(b.jitter),
		LastSenderReport:   uint32(lastSRNTPTime >> 16),
		Delay:              dlsr,
	}
	return rr
}

func (b *Buffer) setSenderReportData(rtpTime uint32, ntpTime uint64) {
	atomic.StoreUint32(&b.lastSRRTPTime, rtpTime)
	atomic.StoreUint64(&b.lastSRNTPTime, ntpTime)
	atomic.StoreInt64(&b.lastSRRecv, time.Now().UnixNano())
}

func (b *Buffer) getRTCP() []rtcp.Packet {
	var pkts []rtcp.Packet

	pkts = append(pkts, &rtcp.ReceiverReport{
		Reports: []rtcp.ReceptionReport{b.buildReceptionReport()},
	})

	if b.remb && !b.tcc {
		pkts = append(pkts, b.buildREMBPacket())
	}

	return pkts
}

func (b *Buffer) getPacket(sn uint16) (rtp.Header, []byte, error) {
	if bufferPkt := b.pktQueue.GetPacket(sn); bufferPkt != nil {
		return bufferPkt.Header, bufferPkt.Payload, nil
	}
	return rtp.Header{}, nil, errPacketNotFound
}

func (b *Buffer) onLostHandler(fn func(nack *rtcp.TransportLayerNack)) {
	if b.nack {
		b.pktQueue.onLost = fn
	}
}

func (b *Buffer) onFeedback(fn func(fb []rtcp.Packet)) {
	b.feedbackCB = fn
}

func (b *Buffer) onTransportWideCC(fn func(sn uint16, timeNS int64, marker bool)) {
	b.feedbackTWCC = fn
}
