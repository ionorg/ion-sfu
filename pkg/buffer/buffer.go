package buffer

import (
	"encoding/binary"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"

	log "github.com/pion/ion-log"
	"github.com/pion/rtcp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
)

const (
	maxSN = 1 << 16
	// default buffer time by ms
	defaultBufferTime = 1000

	reportDelta = 1e9
)

type pendingPackets struct {
	arrivalTime int64
	packet      []byte
}

// Buffer contains all packets
type Buffer struct {
	sync.Mutex
	bucket     *Bucket
	codecType  webrtc.RTPCodecType
	videoPool  *sync.Pool
	audioPool  *sync.Pool
	packetChan chan rtp.Packet
	pPackets   []pendingPackets
	mediaSSRC  uint32
	clockRate  uint32
	maxBitrate uint64
	lastReport int64
	twccExt    uint8
	bound      bool
	closed     bool
	onClose    func()

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
	lastRtcpPacketTime int64  // Time the last RTCP packet was received.
	lastRtcpSrTime     int64  // Time the last RTCP SR was received. Required for DLSR computation.
	packetCount        uint32 // Number of packets received from this source.
	lastTransit        uint32
	maxSeqNo           uint16  // The highest sequence number received in an RTP data packet
	jitter             float64 // An estimate of the statistical variance of the RTP data packet inter-arrival time.
	totalByte          uint64
	// callbacks
	feedbackCB   func([]rtcp.Packet)
	feedbackTWCC func(sn uint16, timeNS int64, marker bool)
}

// BufferOptions provides configuration options for the buffer
type Options struct {
	BufferTime int
	MaxBitRate uint64
}

// NewBuffer constructs a new Buffer
func NewBuffer(ssrc uint32, vp, ap *sync.Pool) *Buffer {
	b := &Buffer{
		mediaSSRC:  ssrc,
		videoPool:  vp,
		audioPool:  ap,
		packetChan: make(chan rtp.Packet, 100),
	}
	return b
}

func (b *Buffer) PacketChan() chan rtp.Packet {
	return b.packetChan
}

func (b *Buffer) Bind(params webrtc.RTPParameters, o Options) {
	b.Lock()
	defer b.Unlock()
	codec := params.Codecs[0]
	b.clockRate = codec.ClockRate
	b.maxBitrate = o.MaxBitRate

	switch {
	case strings.HasPrefix(codec.MimeType, "audio/"):
		b.codecType = webrtc.RTPCodecTypeAudio
		b.bucket = b.audioPool.Get().(*Bucket)
		b.bucket.reset()
	case strings.HasPrefix(codec.MimeType, "video/"):
		b.codecType = webrtc.RTPCodecTypeVideo
		b.bucket = b.videoPool.Get().(*Bucket)
		b.bucket.reset()
	default:
		b.codecType = webrtc.RTPCodecType(0)
	}

	for _, ext := range params.HeaderExtensions {
		if ext.URI == sdp.TransportCCURI {
			b.twccExt = uint8(ext.ID)
			break
		}
	}

	if o.BufferTime <= 0 {
		o.BufferTime = defaultBufferTime
	}

	for _, fb := range codec.RTCPFeedback {
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

	b.bucket.onLost = func(nacks []rtcp.NackPair, askKeyframe bool) {
		pkts := []rtcp.Packet{&rtcp.TransportLayerNack{
			MediaSSRC: b.mediaSSRC,
			Nacks:     nacks,
		}}

		if askKeyframe {
			pkts = append(pkts, &rtcp.PictureLossIndication{
				MediaSSRC: b.mediaSSRC,
			})
		}

		b.feedbackCB(pkts)
	}

	for _, pp := range b.pPackets {
		b.calc(pp.packet, pp.arrivalTime)
	}
	b.pPackets = nil
	b.bound = true

	log.Debugf("NewBuffer BufferOptions=%v", o)
}

// Write adds a RTP Packet, out of order, new packet may be arrived later
func (b *Buffer) Write(pkt []byte) (n int, err error) {
	b.Lock()
	defer b.Unlock()

	if b.closed {
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
		if b.closed {
			err = io.EOF
			return
		}

		b.Lock()
		if b.pPackets != nil && len(b.pPackets) > 0 {
			if len(buff) < len(b.pPackets[0].packet) {
				err = errBufferTooSmall
				b.Unlock()
				return
			}
			n = len(b.pPackets[0].packet)
			copy(buff, b.pPackets[0].packet)
			b.Unlock()
			return
		}
		b.Unlock()
	}
}

func (b *Buffer) Close() error {
	b.Lock()
	defer b.Unlock()

	b.closed = true
	if b.bucket != nil && b.codecType == webrtc.RTPCodecTypeVideo {
		b.videoPool.Put(b.bucket)
	}
	if b.bucket != nil && b.codecType == webrtc.RTPCodecTypeAudio {
		b.audioPool.Put(b.bucket)
	}
	b.onClose()
	close(b.packetChan)
	return nil
}

func (b *Buffer) OnClose(fn func()) {
	b.onClose = fn
}

func (b *Buffer) calc(pkt []byte, arrivalTime int64) {
	sn := binary.BigEndian.Uint16(pkt[2:4])

	if b.packetCount == 0 {
		b.baseSN = sn
		b.maxSeqNo = sn
		b.bucket.headSN = sn - 1
		b.lastReport = arrivalTime
	} else if (sn-b.maxSeqNo)&0x8000 == 0 {
		if sn < b.maxSeqNo {
			b.cycles += maxSN
		}
		b.maxSeqNo = sn
	}
	b.totalByte += uint64(len(pkt))
	b.packetCount++

	var p rtp.Packet
	if err := p.Unmarshal(b.bucket.addPacket(pkt, sn, sn == b.maxSeqNo)); err != nil {
		return
	}
	b.packetChan <- p

	arrival := uint32(arrivalTime / 1e6 * int64(b.clockRate/1e3))
	transit := arrival - p.Timestamp
	if b.lastTransit != 0 {
		d := int32(transit - b.lastTransit)
		if d < 0 {
			d = -d
		}
		b.jitter += (float64(d) - b.jitter) / 16
	}
	b.lastTransit = transit

	if b.tcc {
		if ext := p.GetExtension(b.twccExt); ext != nil && len(ext) > 1 {
			b.feedbackTWCC(binary.BigEndian.Uint16(ext[0:2]), arrivalTime, (pkt[1]>>7&0x1) > 0)
		}
	}
	if arrivalTime-b.lastReport >= reportDelta {
		b.feedbackCB(b.getRTCP())
		b.lastReport = arrivalTime
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
		Jitter:             uint32(b.jitter),
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

	if b.remb && !b.tcc {
		pkts = append(pkts, b.buildREMBPacket())
	}

	return pkts
}

func (b *Buffer) GetPacket(buff []byte, sn uint16) (int, error) {
	b.Lock()
	defer b.Unlock()
	if b.closed {
		return 0, io.EOF
	}
	return b.bucket.getPacket(buff, sn)
}

func (b *Buffer) OnTransportWideCC(fn func(sn uint16, timeNS int64, marker bool)) {
	b.feedbackTWCC = fn
}

func (b *Buffer) OnFeedback(fn func(fb []rtcp.Packet)) {
	b.feedbackCB = fn
}
