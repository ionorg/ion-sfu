package sfu

import (
	"fmt"
	"sync"

	"github.com/pion/webrtc/v3"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

const (
	maxSN      = 65536
	maxPktSize = 1000

	// kProcessIntervalMs=20 ms
	// https://chromium.googlesource.com/external/webrtc/+/ad34dbe934/webrtc/modules/video_coding/nack_module.cc#28

	// vp8 vp9 h264 clock rate 90000Hz
	videoClock = 90000

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
	pktBuffer   [maxSN]*rtp.Packet
	lastNackSN  uint16
	lastClearTS uint32
	lastClearSN uint16
	rtcpCh      chan rtcp.Packet
	// Last seqnum that has been added to buffer
	lastPushSN uint16

	ssrc        uint32
	payloadType uint8

	// calc lost rate
	receivedPkt int
	lostPkt     int

	// calc bandwidth
	totalByte uint64

	// buffer time
	maxBufferTS uint32

	stop bool
	mu   sync.RWMutex

	// lastTCCSN      uint16
	// bufferStartTS time.Time
}

// BufferOptions provides configuration options for the buffer
type BufferOptions struct {
	BufferTime int
}

// NewBuffer constructs a new Buffer
func NewBuffer(ch chan rtcp.Packet, ssrc uint32, pt uint8, o BufferOptions) *Buffer {
	b := &Buffer{
		ssrc:        ssrc,
		payloadType: pt,
		rtcpCh:      ch,
	}

	if o.BufferTime <= 0 {
		o.BufferTime = defaultBufferTime
	}
	b.maxBufferTS = uint32(o.BufferTime) * videoClock / 1000
	// b.bufferStartTS = time.Now()
	log.Debugf("NewBuffer BufferOptions=%v", o)
	return b
}

// Push adds a RTP Packet, out of order, new packet may be arrived later
func (b *Buffer) Push(p *rtp.Packet) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.receivedPkt++
	b.totalByte += uint64(p.MarshalSize())

	// init lastClearTS
	if b.lastClearTS == 0 {
		b.lastClearTS = p.Timestamp
	}

	// init lastClearSN
	if b.lastClearSN == 0 {
		b.lastClearSN = p.SequenceNumber
	}

	// init lastNackSN
	if b.lastNackSN == 0 {
		b.lastNackSN = p.SequenceNumber
	}

	b.pktBuffer[p.SequenceNumber] = p
	b.lastPushSN = p.SequenceNumber

	if b.lastPushSN-b.lastNackSN >= maxNackLostSize {
		// limit nack range
		b.lastNackSN = b.lastPushSN - maxNackLostSize
		// calc [lastNackSN, lastpush-8] if has keyframe
		nackPair, lostPkt := b.GetNackPair(b.pktBuffer, b.lastNackSN, b.lastPushSN)
		// clear old packet by timestamp
		b.clearOldPkt(p.Timestamp, p.SequenceNumber)
		b.lastNackSN = b.lastPushSN
		log.Tracef("b.lastNackSN=%v, b.lastPushSN=%v, lostPkt=%v, nackPair=%v", b.lastNackSN, b.lastPushSN, lostPkt, nackPair)
		if lostPkt > 0 {
			b.lostPkt += lostPkt
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

// Stop buffer
func (b *Buffer) Stop() {
	b.stop = true
}

// GetPayloadType gets the buffers payloadtype
func (b *Buffer) GetPayloadType() uint8 {
	return b.payloadType
}

func (b *Buffer) stats() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return fmt.Sprintf("buffer: [%d, %d] | lastNackSN: %d", b.lastClearSN, b.lastPushSN, b.lastNackSN)
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
			blp |= (1 << (i - lost - 1))
			lostPkt++
		}
	}
	// log.Tracef("NackPair begin=%v end=%v buffer=%v\n", begin, end, buffer[begin:end])
	return rtcp.NackPair{PacketID: lost, LostPackets: rtcp.PacketBitmap(blp)}, lostPkt
}

// GetSSRC get ssrc
func (b *Buffer) GetSSRC() uint32 {
	return b.ssrc
}

// GetLostRateBandwidth calc lostRate and bandwidth by cycle
func (b *Buffer) GetLostRateBandwidth(cycle uint64) (float64, uint64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	lostRate := float64(b.lostPkt) / float64(b.receivedPkt+b.lostPkt)
	byteRate := b.totalByte / cycle
	log.Tracef("Buffer.CalcLostRateByteRate b.receivedPkt=%d b.lostPkt=%d   lostRate=%v byteRate=%v", b.receivedPkt, b.lostPkt, lostRate, byteRate)
	b.receivedPkt, b.lostPkt, b.totalByte = 0, 0, 0
	return lostRate, byteRate * 8
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
