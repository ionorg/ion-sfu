package buffer

import (
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/pion/sdp/v3"

	"github.com/pion/interceptor"

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
	MediaSSRC  uint32
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

	latestTimestamp     uint32 // latest received RTP timestamp on packet
	latestTimestampTime int64  // Time of the latest timestamp (in nanos since unix epoch)

	// callbacks
	feedbackCB   func([]rtcp.Packet)
	feedbackTWCC func(sn uint16, timeNS int64, marker bool)
}

type BufferStats struct {
	LastExpected uint32
	LastReceived uint32
	LostRate     float32
	PacketCount  uint32  // Number of packets received from this source.
	Jitter       float64 // An estimate of the statistical variance of the RTP data packet inter-arrival time.
	TotalByte    uint64
}

// BufferOptions provides configuration options for the buffer
type Options struct {
	BufferTime int
	MaxBitRate uint64
}

// NewBuffer constructs a new Buffer
func NewBuffer(info *interceptor.StreamInfo, o Options) *Buffer {
	b := &Buffer{
		mediaSSRC:  info.SSRC,
		clockRate:  info.ClockRate,
		maxBitrate: o.MaxBitRate,
		simulcast:  false,
	}
	switch {
	case strings.HasPrefix(info.MimeType, "audio/"):
		b.codecType = webrtc.RTPCodecTypeAudio
	case strings.HasPrefix(info.MimeType, "video/"):
		b.codecType = webrtc.RTPCodecTypeVideo
	default:
		b.codecType = webrtc.RTPCodecType(0)
	}

	for _, ext := range info.RTPHeaderExtensions {
		if ext.URI == sdp.TransportCCURI {
			b.twccExt = uint8(ext.ID)
			break
		}
	}

	if o.BufferTime <= 0 {
		o.BufferTime = defaultBufferTime
	}
	b.pktQueue.duration = uint32(o.BufferTime) * b.clockRate / 1000
	b.pktQueue.ssrc = b.mediaSSRC

	for _, fb := range info.RTCPFeedback {
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
	} else if (p.SequenceNumber-b.maxSeqNo)&0x8000 == 0 {
		if p.SequenceNumber < b.maxSeqNo {
			b.cycles += maxSN
		}
		b.maxSeqNo = p.SequenceNumber
	}
	b.packetCount++

	// if first time update or the timestamp is later (factoring timestamp wrap around)
	if (b.latestTimestampTime == 0) || IsLaterTimestamp(p.Timestamp, b.latestTimestamp) {
		b.latestTimestamp = p.Timestamp
		b.latestTimestampTime = b.lastPacketTime
	}

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
		b.pktQueue.addPacket(p, p.SequenceNumber == b.maxSeqNo)
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

func (b *Buffer) onTransportWideCC(fn func(sn uint16, timeNS int64, marker bool)) {
	b.feedbackTWCC = fn
}

func (b *Buffer) onFeedback(fn func(fb []rtcp.Packet)) {
	b.feedbackCB = fn
}

func (b *Buffer) onNack(fn func(fb *rtcp.TransportLayerNack)) {
	b.pktQueue.onLost = fn
}

// GetMediaSSRC returns the associated SSRC of the RTP stream
func (b *Buffer) GetMediaSSRC() uint32 {
	return b.mediaSSRC
}

// GetClockRate returns the RTP clock rate
func (b *Buffer) GetClockRate() uint32 {
	return b.clockRate
}

// GetSenderReportData returns the rtp, ntp and nanos of the last sender report
func (b *Buffer) GetSenderReportData() (rtpTime uint32, ntpTime uint64, lastReceivedTimeInNanosSinceEpoch int64) {
	rtpTime = atomic.LoadUint32(&b.lastSRRTPTime)
	ntpTime = atomic.LoadUint64(&b.lastSRNTPTime)
	lastReceivedTimeInNanosSinceEpoch = atomic.LoadInt64(&b.lastSRRecv)

	return rtpTime, ntpTime, lastReceivedTimeInNanosSinceEpoch
}

// GetStats returns the raw statistics about a particular buffer state
func (b *Buffer) GetStats() (stats BufferStats) {
	stats.LastExpected = atomic.LoadUint32(&b.lastExpected)
	stats.LastReceived = atomic.LoadUint32(&b.lastReceived)
	raw32Lost := atomic.LoadUint32((*uint32)(unsafe.Pointer(&b.lostRate)))
	stats.LostRate = *((*float32)(unsafe.Pointer(&raw32Lost)))
	stats.PacketCount = atomic.LoadUint32(&b.packetCount)

	raw64Jitter := atomic.LoadUint64((*uint64)(unsafe.Pointer(&b.lostRate)))
	stats.Jitter = *((*float64)(unsafe.Pointer(&raw64Jitter)))

	stats.TotalByte = atomic.LoadUint64(&b.totalByte)

	return stats
}

// GetLatestTimestamp returns the latest RTP timestamp factoring in potential RTP timestamp wrap-around
func (b *Buffer) GetLatestTimestamp() (latestTimestamp uint32, latestTimestampTimeInNanosSinceEpoch int64) {
	latestTimestamp = atomic.LoadUint32(&b.latestTimestamp)
	latestTimestampTimeInNanosSinceEpoch = atomic.LoadInt64(&b.latestTimestampTime)

	return latestTimestamp, latestTimestampTimeInNanosSinceEpoch
}

// IsTimestampWrapAround returns true if wrap around happens from timestamp1 to timestamp2
func IsTimestampWrapAround(timestamp1 uint32, timestamp2 uint32) bool {
	return (timestamp1&0xC000000 == 0) && (timestamp2&0xC000000 == 0xC000000)
}

// IsLaterTimestamp returns true if timestamp1 is later in time than timestamp2 factoring in timestamp wrap-around
func IsLaterTimestamp(timestamp1 uint32, timestamp2 uint32) bool {
	if timestamp1 > timestamp2 {
		if IsTimestampWrapAround(timestamp2, timestamp1) {
			return false
		}
		return true
	}
	if IsTimestampWrapAround(timestamp1, timestamp2) {
		return true
	}
	return false
}
