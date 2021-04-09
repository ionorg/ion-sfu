package buffer

import (
	"encoding/binary"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gammazero/deque"
	"github.com/go-logr/logr"
	log "github.com/pion/ion-log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
)

const (
	maxSN = 1 << 16

	reportDelta = 1e9
)

// Logger is an implementation of logr.Logger. If is not provided - will be turned off.
var Logger logr.Logger = new(logr.DiscardLogger)

type pendingPackets struct {
	arrivalTime int64
	packet      []byte
}

type ExtPacket struct {
	Head     bool
	Cycle    uint32
	Arrival  int64
	Packet   rtp.Packet
	Payload  interface{}
	KeyFrame bool
}

// Buffer contains all packets
type Buffer struct {
	sync.Mutex
	bucket     *Bucket
	nacker     *nackQueue
	videoPool  *sync.Pool
	audioPool  *sync.Pool
	codecType  webrtc.RTPCodecType
	extPackets deque.Deque
	pPackets   []pendingPackets
	closeOnce  sync.Once
	mediaSSRC  uint32
	clockRate  uint32
	maxBitrate uint64
	lastReport int64
	twccExt    uint8
	audioExt   uint8
	bound      bool
	closed     atomicBool
	mime       string

	// supported feedbacks
	remb       bool
	nack       bool
	twcc       bool
	audioLevel bool

	minPacketProbe     int
	lastPacketRead     int
	maxTemporalLayer   int64
	bitrate            uint64
	bitrateHelper      uint64
	lastSRNTPTime      uint64
	lastSRRTPTime      uint32
	lastSRRecv         int64 // Represents wall clock of the most recent sender report arrival
	baseSN             uint16
	cycles             uint32
	lastRtcpPacketTime int64 // Time the last RTCP packet was received.
	lastRtcpSrTime     int64 // Time the last RTCP SR was received. Required for DLSR computation.
	lastTransit        uint32
	maxSeqNo           uint16 // The highest sequence number received in an RTP data packet

	stats Stats

	latestTimestamp     uint32 // latest received RTP timestamp on packet
	latestTimestampTime int64  // Time of the latest timestamp (in nanos since unix epoch)

	// callbacks
	onClose      func()
	onAudioLevel func(level uint8)
	feedbackCB   func([]rtcp.Packet)
	feedbackTWCC func(sn uint16, timeNS int64, marker bool)

	// logger
	logger logr.Logger
}

type Stats struct {
	LastExpected uint32
	LastReceived uint32
	LostRate     float32
	PacketCount  uint32  // Number of packets received from this source.
	Jitter       float64 // An estimate of the statistical variance of the RTP data packet inter-arrival time.
	TotalByte    uint64
}

// BufferOptions provides configuration options for the buffer
type Options struct {
	MaxBitRate uint64
}

// NewBuffer constructs a new Buffer
func NewBuffer(ssrc uint32, vp, ap *sync.Pool, logger logr.Logger) *Buffer {
	b := &Buffer{
		mediaSSRC: ssrc,
		videoPool: vp,
		audioPool: ap,
		logger:    logger,
	}
	b.extPackets.SetMinCapacity(7)
	return b
}

func (b *Buffer) Bind(params webrtc.RTPParameters, o Options) {
	b.Lock()
	defer b.Unlock()

	codec := params.Codecs[0]
	b.clockRate = codec.ClockRate
	b.maxBitrate = o.MaxBitRate
	b.mime = strings.ToLower(codec.MimeType)

	switch {
	case strings.HasPrefix(b.mime, "audio/"):
		b.codecType = webrtc.RTPCodecTypeAudio
		b.bucket = NewBucket(b.audioPool.Get().([]byte))
	case strings.HasPrefix(b.mime, "video/"):
		b.codecType = webrtc.RTPCodecTypeVideo
		b.bucket = NewBucket(b.videoPool.Get().([]byte))
	default:
		b.codecType = webrtc.RTPCodecType(0)
	}

	for _, ext := range params.HeaderExtensions {
		if ext.URI == sdp.TransportCCURI {
			b.twccExt = uint8(ext.ID)
			break
		}
	}

	if b.codecType == webrtc.RTPCodecTypeVideo {
		for _, fb := range codec.RTCPFeedback {
			switch fb.Type {
			case webrtc.TypeRTCPFBGoogREMB:
				b.logger.V(1).Info("Setting feedback", "type", "webrtc.TypeRTCPFBGoogREMB")
				b.remb = true
			case webrtc.TypeRTCPFBTransportCC:
				b.logger.V(1).Info("Setting feedback", "type", webrtc.TypeRTCPFBTransportCC)
				b.twcc = true
			case webrtc.TypeRTCPFBNACK:
				b.logger.V(1).Info("Setting feedback", "type", webrtc.TypeRTCPFBNACK)
				b.nacker = newNACKQueue()
				b.nack = true
			}
		}
	} else if b.codecType == webrtc.RTPCodecTypeAudio {
		for _, h := range params.HeaderExtensions {
			if h.URI == sdp.AudioLevelURI {
				b.audioLevel = true
				b.audioExt = uint8(h.ID)
			}
		}
	}

	for _, pp := range b.pPackets {
		b.calc(pp.packet, pp.arrivalTime)
	}
	b.pPackets = nil
	b.bound = true

	b.logger.V(1).Info("NewBuffer", "MaxBitRate", o.MaxBitRate)
}

// Write adds a RTP Packet, out of order, new packet may be arrived later
func (b *Buffer) Write(pkt []byte) (n int, err error) {
	b.Lock()
	defer b.Unlock()

	if b.closed.get() {
		err = io.EOF
		return
	}

	if !b.bound {
		packet := make([]byte, len(pkt))
		copy(packet, pkt)
		b.pPackets = append(b.pPackets, pendingPackets{
			packet:      packet,
			arrivalTime: time.Now().UnixNano(),
		})
		return
	}

	b.calc(pkt, time.Now().UnixNano())

	return
}

func (b *Buffer) Read(buff []byte) (n int, err error) {
	for {
		if b.closed.get() {
			err = io.EOF
			return
		}
		b.Lock()
		if b.pPackets != nil && len(b.pPackets) > b.lastPacketRead {
			if len(buff) < len(b.pPackets[b.lastPacketRead].packet) {
				err = errBufferTooSmall
				b.Unlock()
				return
			}
			n = len(b.pPackets[b.lastPacketRead].packet)
			copy(buff, b.pPackets[b.lastPacketRead].packet)
			b.lastPacketRead++
			b.Unlock()
			return
		}
		b.Unlock()
		time.Sleep(25 * time.Millisecond)
	}
}

func (b *Buffer) ReadExtended() (*ExtPacket, error) {
	for {
		if b.closed.get() {
			return nil, io.EOF
		}
		b.Lock()
		if b.extPackets.Len() > 0 {
			extPkt := b.extPackets.PopFront().(*ExtPacket)
			b.Unlock()
			return extPkt, nil
		}
		b.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
}

func (b *Buffer) Close() error {
	b.Lock()
	defer b.Unlock()

	b.closeOnce.Do(func() {
		if b.bucket != nil && b.codecType == webrtc.RTPCodecTypeVideo {
			b.videoPool.Put(b.bucket.buf)
		}
		if b.bucket != nil && b.codecType == webrtc.RTPCodecTypeAudio {
			b.audioPool.Put(b.bucket.buf)
		}
		b.closed.set(true)
		b.onClose()
	})
	return nil
}

func (b *Buffer) OnClose(fn func()) {
	b.onClose = fn
}

func (b *Buffer) calc(pkt []byte, arrivalTime int64) {
	sn := binary.BigEndian.Uint16(pkt[2:4])

	if b.stats.PacketCount == 0 {
		b.baseSN = sn
		b.maxSeqNo = sn
		b.lastReport = arrivalTime
	} else if (sn-b.maxSeqNo)&0x8000 == 0 {
		if sn < b.maxSeqNo {
			b.cycles += maxSN
		}
		if b.nack {
			diff := sn - b.maxSeqNo
			for i := uint16(1); i < diff; i++ {
				var extSN uint32
				msn := sn - i
				if msn > b.maxSeqNo && msn&0x8000 > 0 && b.maxSeqNo&0x8000 == 0 {
					extSN = (b.cycles - maxSN) | uint32(msn)
				} else {
					extSN = b.cycles | uint32(msn)
				}
				b.nacker.push(extSN)
			}
		}
		b.maxSeqNo = sn
	} else if b.nack && (sn-b.maxSeqNo)&0x8000 > 0 {
		var extSN uint32
		if sn > b.maxSeqNo && sn&0x8000 > 0 && b.maxSeqNo&0x8000 == 0 {
			extSN = (b.cycles - maxSN) | uint32(sn)
		} else {
			extSN = b.cycles | uint32(sn)
		}
		b.nacker.remove(extSN)
	}

	var p rtp.Packet
	pb, err := b.bucket.AddPacket(pkt, sn, sn == b.maxSeqNo)
	if err != nil {
		if err == errRTXPacket {
			return
		}
		log.Errorf("buffer write err: %v", err)
		return
	}
	if err = p.Unmarshal(pb); err != nil {
		return
	}

	b.stats.TotalByte += uint64(len(pkt))
	b.bitrateHelper += uint64(len(pkt))
	b.stats.PacketCount++

	ep := ExtPacket{
		Head:    sn == b.maxSeqNo,
		Cycle:   b.cycles,
		Packet:  p,
		Arrival: arrivalTime,
	}

	switch b.mime {
	case "video/vp8":
		vp8Packet := VP8{}
		if err := vp8Packet.Unmarshal(p.Payload); err != nil {
			return
		}
		ep.Payload = vp8Packet
		ep.KeyFrame = vp8Packet.IsKeyFrame
	case "video/h264":
		ep.KeyFrame = isH264Keyframe(p.Payload)
	}

	if b.minPacketProbe < 25 {
		if sn < b.baseSN {
			b.baseSN = sn
		}

		if b.mime == "video/vp8" {
			pld := ep.Payload.(VP8)
			mtl := atomic.LoadInt64(&b.maxTemporalLayer)
			if mtl < int64(pld.TID) {
				atomic.StoreInt64(&b.maxTemporalLayer, int64(pld.TID))
			}
		}

		b.minPacketProbe++
	}

	b.extPackets.PushBack(&ep)

	// if first time update or the timestamp is later (factoring timestamp wrap around)
	if (b.latestTimestampTime == 0) || IsLaterTimestamp(p.Timestamp, b.latestTimestamp) {
		b.latestTimestamp = p.Timestamp
		b.latestTimestampTime = arrivalTime
	}

	arrival := uint32(arrivalTime / 1e6 * int64(b.clockRate/1e3))
	transit := arrival - p.Timestamp
	if b.lastTransit != 0 {
		d := int32(transit - b.lastTransit)
		if d < 0 {
			d = -d
		}
		b.stats.Jitter += (float64(d) - b.stats.Jitter) / 16
	}
	b.lastTransit = transit

	if b.twcc {
		if ext := p.GetExtension(b.twccExt); ext != nil && len(ext) > 1 {
			b.feedbackTWCC(binary.BigEndian.Uint16(ext[0:2]), arrivalTime, p.Marker)
		}
	}

	if b.audioLevel {
		if e := p.GetExtension(b.audioExt); e != nil && b.onAudioLevel != nil {
			ext := rtp.AudioLevelExtension{}
			if err := ext.Unmarshal(e); err == nil {
				b.onAudioLevel(ext.Level)
			}
		}
	}

	diff := arrivalTime - b.lastReport

	if b.nacker != nil {
		if r := b.buildNACKPacket(); r != nil {
			b.feedbackCB(r)
		}
	}

	if diff >= reportDelta {
		br := (8 * b.bitrateHelper * uint64(reportDelta)) / uint64(diff)
		atomic.StoreUint64(&b.bitrate, br)
		b.feedbackCB(b.getRTCP())
		b.lastReport = arrivalTime
		b.bitrateHelper = 0
	}
}

func (b *Buffer) buildNACKPacket() []rtcp.Packet {
	if nacks, askKeyframe := b.nacker.pairs(b.cycles | uint32(b.maxSeqNo)); (nacks != nil && len(nacks) > 0) || askKeyframe {
		var pkts []rtcp.Packet
		if len(nacks) > 0 {
			pkts = []rtcp.Packet{&rtcp.TransportLayerNack{
				MediaSSRC: b.mediaSSRC,
				Nacks:     nacks,
			}}
		}

		if askKeyframe {
			pkts = append(pkts, &rtcp.PictureLossIndication{
				MediaSSRC: b.mediaSSRC,
			})
		}
		return pkts
	}
	return nil
}

func (b *Buffer) buildREMBPacket() *rtcp.ReceiverEstimatedMaximumBitrate {
	br := b.bitrate
	if b.stats.LostRate < 0.02 {
		br = uint64(float64(br)*1.09) + 2000
	}
	if b.stats.LostRate > .1 {
		br = uint64(float64(br) * float64(1-0.5*b.stats.LostRate))
	}
	if br > b.maxBitrate {
		br = b.maxBitrate
	}
	if br < 100000 {
		br = 100000
	}
	b.stats.TotalByte = 0

	return &rtcp.ReceiverEstimatedMaximumBitrate{
		Bitrate: br,
		SSRCs:   []uint32{b.mediaSSRC},
	}
}

func (b *Buffer) buildReceptionReport() rtcp.ReceptionReport {
	extMaxSeq := b.cycles | uint32(b.maxSeqNo)
	expected := extMaxSeq - uint32(b.baseSN) + 1
	lost := uint32(0)
	if b.stats.PacketCount < expected && b.stats.PacketCount != 0 {
		lost = expected - b.stats.PacketCount
	}
	expectedInterval := expected - b.stats.LastExpected
	b.stats.LastExpected = expected

	receivedInterval := b.stats.PacketCount - b.stats.LastReceived
	b.stats.LastReceived = b.stats.PacketCount

	lostInterval := expectedInterval - receivedInterval

	b.stats.LostRate = float32(lostInterval) / float32(expectedInterval)
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
		SSRC:               b.mediaSSRC,
		FractionLost:       fracLost,
		TotalLost:          lost,
		LastSequenceNumber: extMaxSeq,
		Jitter:             uint32(b.stats.Jitter),
		LastSenderReport:   uint32(b.lastSRNTPTime >> 16),
		Delay:              dlsr,
	}
	return rr
}

func (b *Buffer) SetSenderReportData(rtpTime uint32, ntpTime uint64) {
	b.Lock()
	b.lastSRRTPTime = rtpTime
	b.lastSRNTPTime = ntpTime
	b.lastSRRecv = time.Now().UnixNano()
	b.Unlock()
}

func (b *Buffer) getRTCP() []rtcp.Packet {
	var pkts []rtcp.Packet

	pkts = append(pkts, &rtcp.ReceiverReport{
		Reports: []rtcp.ReceptionReport{b.buildReceptionReport()},
	})

	if b.remb && !b.twcc {
		pkts = append(pkts, b.buildREMBPacket())
	}

	return pkts
}

func (b *Buffer) GetPacket(buff []byte, sn uint16) (int, error) {
	b.Lock()
	defer b.Unlock()
	if b.closed.get() {
		return 0, io.EOF
	}
	return b.bucket.GetPacket(buff, sn)
}

// Bitrate returns the current publisher stream bitrate.
func (b *Buffer) Bitrate() uint64 {
	return atomic.LoadUint64(&b.bitrate)
}

func (b *Buffer) MaxTemporalLayer() int64 {
	return atomic.LoadInt64(&b.maxTemporalLayer)
}

func (b *Buffer) OnTransportWideCC(fn func(sn uint16, timeNS int64, marker bool)) {
	b.feedbackTWCC = fn
}

func (b *Buffer) OnFeedback(fn func(fb []rtcp.Packet)) {
	b.feedbackCB = fn
}

func (b *Buffer) OnAudioLevel(fn func(level uint8)) {
	b.onAudioLevel = fn
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
func (b *Buffer) GetStats() (stats Stats) {
	b.Lock()
	stats = b.stats
	b.Unlock()
	return
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
