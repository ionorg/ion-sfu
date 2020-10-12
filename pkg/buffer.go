package sfu

import (
	"sync"
	"time"

	"github.com/pion/webrtc/v3"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

const (
	maxSN = 1 << 16
	// 1+16(FSN+BLP) https://tools.ietf.org/html/rfc2032#page-9
	maxNackLostSize = 17
	// default buffer time by ms
	defaultBufferTime = 1000
)

func tsDelta(x, y uint32) uint32 {
	if x > y {
		return x - y
	}
	return y - x
}

// Buffer contains all packets
type Buffer struct {
	pktBuffer     [maxSN]*rtp.Packet
	rtcpCh        chan rtcp.Packet
	lastNackSN    uint16
	lastClearTS   uint32
	lastClearSN   uint16
	lastSRNTPTime uint64
	lastSRRTPTime uint32
	lastSRRecv    int64 // Represents wall clock of the most recent sender report arrival
	receivedPkts  uint64
	lastSN        uint16
	baseSN        uint16
	cycles        uint32
	lastExpected  uint32
	lastReceived  uint32

	ssrc        uint32
	payloadType uint8
	clockRate   uint32
	// stats required to generate receiver reports
	// ref: https://www.rfc-editor.org/rfc/rfc3550.html
	totalByte       uint64
	lastPacketTime, // time the last RTP packet from this source was received
	lastRtcpPacketTime, // time the last RTCP packet was received.
	lastRtcpSrTime int64 // time the last RTCP SR was received. Required for DLSR computation.
	packetCount, // number of packets received from this source.
	octetCount, // number of octets received from this source.
	extendedMaxSeqNum,
	lastTransit,
	cumulativePacketLost uint32 // The total of RTP packets that have been lost since the beginning of reception.
	maxSeqNo     uint16 // The highest sequence number received in an RTP data packet
	fractionLost uint8  // The fraction packets from source lost since the previous SR or RR packet was sent
	jitter       uint32 // An estimate of the statistical variance of the RTP data packet inter-arrival time.

	// buffer time
	maxBufferTS uint32

	mu sync.RWMutex

	// lastTCCSN      uint16
	// bufferStartTS time.Time
}

// BufferOptions provides configuration options for the buffer
type BufferOptions struct {
	BufferTime int
}

// NewBuffer constructs a new Buffer
func NewBuffer(ch chan rtcp.Packet, track *webrtc.Track, o BufferOptions) *Buffer {
	b := &Buffer{
		ssrc:        track.SSRC(),
		payloadType: track.PayloadType(),
		clockRate:   track.Codec().ClockRate,
		rtcpCh:      ch,
	}

	if o.BufferTime <= 0 {
		o.BufferTime = defaultBufferTime
	}
	b.maxBufferTS = uint32(o.BufferTime) * b.clockRate / 1000
	log.Debugf("NewBuffer BufferOptions=%v", o)
	return b
}

// Push adds a RTP Packet, out of order, new packet may be arrived later
func (b *Buffer) Push(p *rtp.Packet) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.receivedPkts++
	b.totalByte += uint64(p.MarshalSize())

	if b.packetCount == 0 {
		b.lastClearTS = p.Timestamp
		b.lastClearSN = p.SequenceNumber
		b.lastNackSN = p.SequenceNumber
		b.baseSN = p.SequenceNumber
		b.maxSeqNo = p.SequenceNumber
	} else {
		if seqNoDiff(b.maxSeqNo, p.SequenceNumber) <= 0 {
			if p.SequenceNumber < b.maxSeqNo {
				b.cycles += maxSN
			}
			b.maxSeqNo = p.SequenceNumber
		}
	}

	b.pktBuffer[p.SequenceNumber] = p
	b.lastSN = p.SequenceNumber
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
	if b.lastSN-b.lastNackSN >= maxNackLostSize {
		// limit nack range
		b.lastNackSN = b.lastSN - maxNackLostSize
		// calc [lastNackSN, lastpush-8] if has keyframe
		nackPair, lostPkt := b.GetNackPair(b.pktBuffer, b.lastNackSN, b.lastSN)
		// clear old packet by timestamp
		b.clearOldPkt(p.Timestamp, p.SequenceNumber)
		b.lastNackSN = b.lastSN
		log.Tracef("b.lastNackSN=%v, b.lastSN=%v, lostPkt=%v, nackPair=%v", b.lastNackSN, b.lastSN, lostPkt, nackPair)
		if lostPkt > 0 {
			nack := &rtcp.TransportLayerNack{
				// origin ssrc
				// SenderSSRC: b.ssrc,
				MediaSSRC: b.ssrc,
				Nacks: []rtcp.NackPair{
					nackPair,
				},
			}
			b.rtcpCh <- nack
		}
	}
}

func (b *Buffer) buildRR() rtcp.ReceptionReport {
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

	var fracLost byte
	if expectedInterval != 0 && lostInterval > 0 {
		fracLost = byte((lostInterval << 8) / expectedInterval)
	}
	delayMS := uint32((time.Now().UnixNano() - b.lastSRRecv) / 1e6)
	dlsr := (delayMS / 1e3) << 16
	dlsr |= (delayMS % 1e3) * 65536 / 1000

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

func (b *Buffer) setSR(rtpTime uint32, ntpTime uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastSRRTPTime = rtpTime
	b.lastSRNTPTime = ntpTime
	b.lastSRRecv = time.Now().UnixNano()
}

// clearOldPkt clear old packet
func (b *Buffer) clearOldPkt(pushPktTS uint32, pushPktSN uint16) {
	clearTS := b.lastClearTS
	clearSN := b.lastClearSN
	log.Tracef("clearOldPkt pushPktTS=%d pushPktSN=%d     clearTS=%d  clearSN=%d ", pushPktTS, pushPktSN, clearTS, clearSN)
	if tsDelta(pushPktTS, clearTS) >= b.maxBufferTS {
		// pushPktSN will loop from 0 to 65535
		if pushPktSN == 0 {
			// make sure clear the old packet from 655xx to 65535
			pushPktSN = maxSN - 1
		}
		var skipCount int
		for i := clearSN + 1; i <= pushPktSN; i++ {
			if b.pktBuffer[i] == nil {
				skipCount++
				continue
			}
			if tsDelta(pushPktTS, b.pktBuffer[i].Timestamp) >= b.maxBufferTS {
				b.lastClearTS = b.pktBuffer[i].Timestamp
				b.lastClearSN = i
				b.pktBuffer[i] = nil
			} else {
				break
			}
		}
		if skipCount > 0 {
			log.Tracef("b.pktBuffer nil count : %d", skipCount)
		}
		if pushPktSN == maxSN-1 {
			b.lastClearSN = 0
			b.lastNackSN = 0
		}
	}
}

// GetPayloadType gets the buffers payloadtype
func (b *Buffer) GetPayloadType() uint8 {
	return b.payloadType
}

// GetNackPair calc nackpair
func (b *Buffer) GetNackPair(buffer [65536]*rtp.Packet, begin, end uint16) (rtcp.NackPair, int) {
	var lostPkt int

	// size is <= 17
	if end-begin > maxNackLostSize {
		return rtcp.NackPair{}, lostPkt
	}

	// Bitmask of following lost packets (BLP)
	blp := uint16(0)
	lost := uint16(0)

	// find first lost pkt
	for i := begin; i < end; i++ {
		if buffer[i] == nil {
			lost = i
			lostPkt++
			break
		}
	}

	// no packet lost
	if lost == 0 {
		return rtcp.NackPair{}, lostPkt
	}

	// calc blp
	for i := lost; i < end; i++ {
		// calc from next lost packet
		if i > lost && buffer[i] == nil {
			blp |= 1 << (i - lost - 1)
			lostPkt++
		}
	}
	// log.Tracef("NackPair begin=%v end=%v buffer=%v\n", begin, end, buffer[begin:end])
	return rtcp.NackPair{PacketID: lost, LostPackets: rtcp.PacketBitmap(blp)}, lostPkt
}

// GetPacket gets packet by sequence number
func (b *Buffer) GetPacket(sn uint16) *rtp.Packet {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.pktBuffer[sn]
}

// WritePacket write buffer packet to requested track. and modify headers
func (b *Buffer) WritePacket(sn uint16, track *webrtc.Track, snOffset uint16, tsOffset, ssrc uint32) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if bufferPkt := b.pktBuffer[sn]; bufferPkt != nil {
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
