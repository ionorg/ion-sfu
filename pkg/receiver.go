package sfu

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

const (
	// bandwidth range(kbps)
	minBandwidth = 200
	maxSize      = 1024

	// tcc stuff
	tccExtMapID = 3
	//64ms = 64000us = 250 << 8
	//https://webrtc.googlesource.com/src/webrtc/+/f54860e9ef0b68e182a01edc994626d21961bc4b/modules/rtp_rtcp/source/rtcp_packet/transport_feedback.cc#41
	baseScaleFactor = 64000
	//https://webrtc.googlesource.com/src/webrtc/+/f54860e9ef0b68e182a01edc994626d21961bc4b/modules/rtp_rtcp/source/rtcp_packet/transport_feedback.cc#43
	timeWrapPeriodUs = (int64(1) << 24) * baseScaleFactor
)

type rtpExtInfo struct {
	//transport sequence num
	TSN       uint16
	Timestamp int64
}

// Receiver defines a interface for a track receiver
type Receiver interface {
	Track() *webrtc.Track
	GetPacket(sn uint16) *rtp.Packet
	ReadRTP() (*rtp.Packet, error)
	ReadRTCP() (rtcp.Packet, error)
	WriteRTCP(rtcp.Packet) error
	Close()
	stats() string
}

// WebRTCAudioReceiver receives a audio track
type WebRTCAudioReceiver struct {
	track *webrtc.Track
}

// NewWebRTCAudioReceiver creates a new audio track receiver
func NewWebRTCAudioReceiver(track *webrtc.Track) *WebRTCAudioReceiver {
	a := &WebRTCAudioReceiver{
		track: track,
	}

	return a
}

// ReadRTP read rtp packet
func (a *WebRTCAudioReceiver) ReadRTP() (*rtp.Packet, error) {
	return a.track.ReadRTP()
}

// ReadRTCP read rtcp packet
func (a *WebRTCAudioReceiver) ReadRTCP() (rtcp.Packet, error) {
	return nil, errMethodNotSupported
}

// WriteRTCP write rtcp packet
func (a *WebRTCAudioReceiver) WriteRTCP(pkt rtcp.Packet) error {
	return errMethodNotSupported
}

// Track returns receiver track
func (a *WebRTCAudioReceiver) Track() *webrtc.Track {
	return a.track
}

// GetPacket returns nil since audio isn't buffered (uses fec)
func (a *WebRTCAudioReceiver) GetPacket(sn uint16) *rtp.Packet {
	return nil
}

// Close track
func (a *WebRTCAudioReceiver) Close() {}

// Stats get stats for video receiver
func (a *WebRTCAudioReceiver) stats() string {
	return fmt.Sprintf("payload: %d", a.track.PayloadType())
}

// WebRTCVideoReceiver receives a video track
type WebRTCVideoReceiver struct {
	ctx            context.Context
	cancel         context.CancelFunc
	buffer         *Buffer
	track          *webrtc.Track
	bandwidth      uint64
	lostRate       float64
	rtpCh          chan *rtp.Packet
	rtcpCh         chan rtcp.Packet
	rtpExtInfoChan chan rtpExtInfo

	pliCycle     int
	rembCycle    int
	tccCycle     int
	maxBandwidth int
	feedback     string
}

// WebRTCVideoReceiverConfig .
type WebRTCVideoReceiverConfig struct {
	REMBCycle       int `mapstructure:"rembcycle"`
	PLICycle        int `mapstructure:"plicycle"`
	TCCCycle        int `mapstructure:"tcccycle"`
	MaxBandwidth    int `mapstructure:"maxbandwidth"`
	MaxBufferTime   int `mapstructure:"maxbuffertime"`
	ReceiveRTPCycle int `mapstructure:"rtpcycle"`
}

// NewWebRTCVideoReceiver creates a new video track receiver
func NewWebRTCVideoReceiver(ctx context.Context, config WebRTCVideoReceiverConfig, track *webrtc.Track) *WebRTCVideoReceiver {
	ctx, cancel := context.WithCancel(ctx)

	rembCycle := config.REMBCycle
	if rembCycle == 0 {
		rembCycle = 1
	}

	pliCycle := config.PLICycle
	if pliCycle == 0 {
		pliCycle = 1
	}

	tccCycle := config.TCCCycle
	if tccCycle == 0 {
		tccCycle = 1
	}

	v := &WebRTCVideoReceiver{
		ctx:    ctx,
		cancel: cancel,
		buffer: NewBuffer(track.SSRC(), track.PayloadType(), BufferOptions{
			BufferTime: config.MaxBufferTime,
		}),
		track:          track,
		rtpCh:          make(chan *rtp.Packet, maxSize),
		rtcpCh:         make(chan rtcp.Packet, maxSize),
		rtpExtInfoChan: make(chan rtpExtInfo, maxSize),
		rembCycle:      rembCycle,
		pliCycle:       pliCycle,
		tccCycle:       tccCycle,
		maxBandwidth:   config.MaxBandwidth,
	}

	for _, feedback := range track.Codec().RTCPFeedback {
		switch feedback.Type {
		case webrtc.TypeRTCPFBTransportCC:
			log.Debugf("Setting feedback %s", webrtc.TypeRTCPFBTransportCC)
			v.feedback = webrtc.TypeRTCPFBTransportCC
			go v.tccLoop()
		case webrtc.TypeRTCPFBGoogREMB:
			log.Debugf("Setting feedback %s", webrtc.TypeRTCPFBGoogREMB)
			v.feedback = webrtc.TypeRTCPFBGoogREMB
			go v.rembLoop()
		}
	}

	go v.receiveRTP()
	go v.pliLoop()
	go v.bufferRtcpLoop()

	return v
}

// ReadRTP read rtp packets
func (v *WebRTCVideoReceiver) ReadRTP() (*rtp.Packet, error) {
	select {
	case pkt := <-v.rtpCh:
		return pkt, nil
	case <-v.ctx.Done():
		close(v.rtpCh)
		return nil, io.EOF
	}
}

// ReadRTCP read rtcp packets
func (v *WebRTCVideoReceiver) ReadRTCP() (rtcp.Packet, error) {
	select {
	case pkt := <-v.rtcpCh:
		return pkt, nil
	case <-v.ctx.Done():
		close(v.rtcpCh)
		return nil, io.ErrClosedPipe
	}
}

// WriteRTCP write rtcp packet
func (v *WebRTCVideoReceiver) WriteRTCP(pkt rtcp.Packet) error {
	v.rtcpCh <- pkt
	return nil
}

// Track returns receiver track
func (v *WebRTCVideoReceiver) Track() *webrtc.Track {
	return v.track
}

// GetPacket get a buffered packet if we have one
func (v *WebRTCVideoReceiver) GetPacket(sn uint16) *rtp.Packet {
	return v.buffer.GetPacket(sn)
}

// Close track
func (v *WebRTCVideoReceiver) Close() {
	v.cancel()
}

// receiveRTP receive all incoming tracks' rtp and sent to one channel
func (v *WebRTCVideoReceiver) receiveRTP() {
	for {
		pkt, err := v.track.ReadRTP()

		if err == io.EOF {
			return
		}

		if err != nil {
			log.Errorf("rtp err => %v", err)
			continue
		}

		v.buffer.Push(pkt)

		if v.tccCycle > 0 {
			//store arrival time
			timestampUs := time.Now().UnixNano() / 1000
			rtpTCC := rtp.TransportCCExtension{}
			err = rtpTCC.Unmarshal(pkt.GetExtension(tccExtMapID))
			if err == nil {
				// if time.Now().Sub(b.bufferStartTS) > time.Second {

				//only calc the packet which rtpTCC.TransportSequence > b.lastTCCSN
				//https://webrtc.googlesource.com/src/webrtc/+/f54860e9ef0b68e182a01edc994626d21961bc4b/modules/rtp_rtcp/source/rtcp_packet/transport_feedback.cc#353
				// if rtpTCC.TransportSequence > b.lastTCCSN {
				v.rtpExtInfoChan <- rtpExtInfo{
					TSN:       rtpTCC.TransportSequence,
					Timestamp: timestampUs,
				}
				// b.lastTCCSN = rtpTCC.TransportSequence
				// }
			}
		}

		v.rtpCh <- pkt

		if err != nil {
			log.Errorf("jb err => %v", err)
		}
	}
}

func (v *WebRTCVideoReceiver) pliLoop() {
	t := time.NewTicker(time.Duration(v.pliCycle) * time.Second)
	for {
		select {
		case <-t.C:
			pli := &rtcp.PictureLossIndication{SenderSSRC: v.track.SSRC(), MediaSSRC: v.track.SSRC()}
			// log.Infof("pliLoop send pli=%d pt=%v", buffer.GetSSRC(), buffer.GetPayloadType())
			v.rtcpCh <- pli
		case <-v.ctx.Done():
			t.Stop()
			return
		}
	}
}

func (v *WebRTCVideoReceiver) bufferRtcpLoop() {
	for {
		select {
		case pkt := <-v.buffer.GetRTCPChan():
			v.rtcpCh <- pkt
		case <-v.ctx.Done():
			v.buffer.Stop()
			return
		}
	}
}

func (v *WebRTCVideoReceiver) rembLoop() {
	t := time.NewTicker(time.Duration(v.rembCycle) * time.Second)
	for {
		select {
		case <-t.C:
			// only calc video recently
			v.lostRate, v.bandwidth = v.buffer.GetLostRateBandwidth(uint64(v.rembCycle))
			var bw uint64
			switch {
			case v.lostRate == 0 && v.bandwidth == 0:
				bw = uint64(v.maxBandwidth)
			case v.lostRate >= 0 && v.lostRate < 0.1:
				bw = uint64(v.bandwidth * 2)
			default:
				bw = uint64(float64(v.bandwidth) * (1 - v.lostRate))
			}

			if bw > uint64(v.maxBandwidth) {
				bw = uint64(v.maxBandwidth)
			}

			remb := &rtcp.ReceiverEstimatedMaximumBitrate{
				SenderSSRC: v.buffer.GetSSRC(),
				Bitrate:    bw * 1000,
				SSRCs:      []uint32{v.buffer.GetSSRC()},
			}

			v.rtcpCh <- remb
		case <-v.ctx.Done():
			t.Stop()
			return
		}
	}
}

func (v *WebRTCVideoReceiver) tccLoop() {
	feedbackPacketCount := uint8(0)
	t := time.NewTicker(time.Duration(v.tccCycle) * time.Millisecond)
	for {
		select {
		case <-t.C:
			cap := len(v.rtpExtInfoChan)
			if cap == 0 {
				continue
			}

			// get all rtp extension infos from channel
			rtpExtInfo := make(map[uint16]int64)
			for i := 0; i < cap; i++ {
				info := <-v.rtpExtInfoChan
				rtpExtInfo[info.TSN] = info.Timestamp
			}

			//find the min and max transport sn
			var minTSN, maxTSN uint16
			for tsn := range rtpExtInfo {

				//init
				if minTSN == 0 {
					minTSN = tsn
				}

				if minTSN > tsn {
					minTSN = tsn
				}

				if maxTSN < tsn {
					maxTSN = tsn
				}
			}

			//force small deta rtcp.RunLengthChunk
			chunk := &rtcp.RunLengthChunk{
				Type:               rtcp.TypeTCCRunLengthChunk,
				PacketStatusSymbol: rtcp.TypeTCCPacketReceivedSmallDelta,
				RunLength:          maxTSN - minTSN + 1,
			}

			//gather deltas
			var recvDeltas []*rtcp.RecvDelta
			var refTime uint32
			var lastTS int64
			var baseTimeTicks int64
			for i := minTSN; i <= maxTSN; i++ {
				ts, ok := rtpExtInfo[i]

				//lost packet
				if !ok {
					recvDelta := &rtcp.RecvDelta{
						Type: rtcp.TypeTCCPacketReceivedSmallDelta,
					}
					recvDeltas = append(recvDeltas, recvDelta)
					continue
				}

				// init lastTS
				if lastTS == 0 {
					lastTS = ts
				}

				//received packet
				if baseTimeTicks == 0 {
					baseTimeTicks = (ts % timeWrapPeriodUs) / baseScaleFactor
				}

				var delta int64
				if lastTS == ts {
					delta = ts%timeWrapPeriodUs - baseTimeTicks*baseScaleFactor
				} else {
					delta = (ts - lastTS) % timeWrapPeriodUs
				}

				if refTime == 0 {
					refTime = uint32(baseTimeTicks) & 0x007FFFFF
				}

				recvDelta := &rtcp.RecvDelta{
					Type:  rtcp.TypeTCCPacketReceivedSmallDelta,
					Delta: delta,
				}
				recvDeltas = append(recvDeltas, recvDelta)
			}
			rtcpTCC := &rtcp.TransportLayerCC{
				Header: rtcp.Header{
					Padding: false,
					Count:   rtcp.FormatTCC,
					Type:    rtcp.TypeTransportSpecificFeedback,
					// Length:  5, //need calc
				},
				// SenderSSRC:         v.ssrc,
				MediaSSRC:          v.track.SSRC(),
				BaseSequenceNumber: minTSN,
				PacketStatusCount:  maxTSN - minTSN + 1,
				ReferenceTime:      refTime,
				FbPktCount:         feedbackPacketCount,
				RecvDeltas:         recvDeltas,
				PacketChunks:       []rtcp.PacketStatusChunk{chunk},
			}
			rtcpTCC.Header.Length = rtcpTCC.Len()/4 - 1
			v.rtcpCh <- rtcpTCC
			feedbackPacketCount++
		case <-v.ctx.Done():
			t.Stop()
			return
		}
	}
}

// Stats get stats for video receiver
func (v *WebRTCVideoReceiver) stats() string {
	return fmt.Sprintf("payload: %d | lostRate: %.2f | bandwidth: %dkbps | %s", v.buffer.GetPayloadType(), v.lostRate, v.bandwidth, v.buffer.stats())
}
