package sfu

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

const (
	// bandwidth range(kbps)
	// minBandwidth = 200
	maxSize = 1024

	// tcc stuff
	tccExtMapID = 3
	// 64ms = 64000us = 250 << 8
	// https://webrtc.googlesource.com/src/webrtc/+/f54860e9ef0b68e182a01edc994626d21961bc4b/modules/rtp_rtcp/source/rtcp_packet/transport_feedback.cc#41
	baseScaleFactor = 64000
	// https://webrtc.googlesource.com/src/webrtc/+/f54860e9ef0b68e182a01edc994626d21961bc4b/modules/rtp_rtcp/source/rtcp_packet/transport_feedback.cc#43
	timeWrapPeriodUs = (int64(1) << 24) * baseScaleFactor
)

type rtpExtInfo struct {
	// transport sequence num
	TSN       uint16
	Timestamp int64
}

// Receiver defines a interface for a track receiver
type Receiver interface {
	Track() *webrtc.Track
	GetPacket(sn uint16) *rtp.Packet
	ReadRTP() chan *rtp.Packet
	ReadRTCP() chan rtcp.Packet
	WriteRTCP(rtcp.Packet) error
	OnCloseHandler(fn func())
	Close()
	stats() string
}

// WebRTCReceiver receives a video track
type WebRTCReceiver struct {
	ctx            context.Context
	cancel         context.CancelFunc
	buffer         *Buffer
	track          *webrtc.Track
	bandwidth      uint64
	lostRate       float64
	rtpCh          chan *rtp.Packet
	rtcpCh         chan rtcp.Packet
	rtpExtInfoChan chan rtpExtInfo
	onCloseHandler func()

	pliCycle     int
	maxBandwidth uint64
	feedback     string
	wg           sync.WaitGroup
}

// WebRTCVideoReceiverConfig .
type WebRTCVideoReceiverConfig struct {
	REMBCycle       int `mapstructure:"rembcycle"`
	PLICycle        int `mapstructure:"plicycle"`
	TCCCycle        int `mapstructure:"tcccycle"`
	MaxBufferTime   int `mapstructure:"maxbuffertime"`
	ReceiveRTPCycle int `mapstructure:"rtpcycle"`
}

// NewWebRTCReceiver creates a new webrtc track receiver
func NewWebRTCReceiver(ctx context.Context, track *webrtc.Track) Receiver {
	ctx, cancel := context.WithCancel(ctx)

	v := &WebRTCReceiver{
		ctx:            ctx,
		cancel:         cancel,
		track:          track,
		rtpCh:          make(chan *rtp.Packet, maxSize),
		rtpExtInfoChan: make(chan rtpExtInfo, maxSize),
	}

	waitStart := make(chan struct{})
	switch track.Kind() {
	case webrtc.RTPCodecTypeVideo:
		go startVideoReceiver(v, waitStart)
	case webrtc.RTPCodecTypeAudio:
		go startAudioReceiver(v, waitStart)
	}
	<-waitStart
	return v
}

// OnCloseHandler method to be called on remote tracked removed
func (v *WebRTCReceiver) OnCloseHandler(fn func()) {
	v.onCloseHandler = fn
}

// ReadRTP read rtp packets
func (v *WebRTCReceiver) ReadRTP() chan *rtp.Packet {
	return v.rtpCh
}

// ReadRTCP read rtcp packets
func (v *WebRTCReceiver) ReadRTCP() chan rtcp.Packet {
	return v.rtcpCh
}

// WriteRTCP write rtcp packet
func (v *WebRTCReceiver) WriteRTCP(pkt rtcp.Packet) error {
	if v.ctx.Err() != nil || v.rtcpCh == nil {
		return io.ErrClosedPipe
	}
	v.rtcpCh <- pkt
	return nil
}

// Track returns receiver track
func (v *WebRTCReceiver) Track() *webrtc.Track {
	return v.track
}

// GetPacket get a buffered packet if we have one
func (v *WebRTCReceiver) GetPacket(sn uint16) *rtp.Packet {
	if v.buffer == nil {
		return nil
	}
	return v.buffer.GetPacket(sn)
}

// Close gracefully close the track
func (v *WebRTCReceiver) Close() {
	if v.ctx.Err() != nil {
		return
	}
	v.cancel()
}

// receiveRTP receive all incoming tracks' rtp and sent to one channel
func (v *WebRTCReceiver) receiveRTP() {
	defer v.wg.Done()
	for {
		pkt, err := v.track.ReadRTP()
		// EOF signal received, this means that the remote track has been removed
		// or the peer has been disconnected. The router must be gracefully shutdown,
		// waiting for all the receiver routines to stop.
		if err == io.EOF {
			v.Close()
			return
		}

		if err != nil {
			log.Errorf("rtp err => %v", err)
			continue
		}

		v.buffer.Push(pkt)

		if v.feedback == webrtc.TypeRTCPFBTransportCC {
			// store arrival time
			timestampUs := time.Now().UnixNano() / 1000
			rtpTCC := rtp.TransportCCExtension{}
			err = rtpTCC.Unmarshal(pkt.GetExtension(tccExtMapID))
			if err == nil {
				// if time.Now().Sub(b.bufferStartTS) > time.Second {

				// only calc the packet which rtpTCC.TransportSequence > b.lastTCCSN
				// https://webrtc.googlesource.com/src/webrtc/+/f54860e9ef0b68e182a01edc994626d21961bc4b/modules/rtp_rtcp/source/rtcp_packet/transport_feedback.cc#353
				// if rtpTCC.TransportSequence > b.lastTCCSN {
				v.rtpExtInfoChan <- rtpExtInfo{
					TSN:       rtpTCC.TransportSequence,
					Timestamp: timestampUs,
				}
				// b.lastTCCSN = rtpTCC.TransportSequence
				// }
			}
		}

		select {
		case <-v.ctx.Done():
			return
		default:
			v.rtpCh <- pkt
		}
	}
}

func (v *WebRTCReceiver) pliLoop() {
	defer v.wg.Done()
	t := time.NewTicker(time.Duration(v.pliCycle) * time.Second)
	for {
		select {
		case <-t.C:
			pli := &rtcp.PictureLossIndication{SenderSSRC: v.track.SSRC(), MediaSSRC: v.track.SSRC()}
			v.rtcpCh <- pli
		case <-v.ctx.Done():
			t.Stop()
			return
		}
	}
}

func (v *WebRTCReceiver) bufferRtcpLoop() {
	defer v.wg.Done()
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

func (v *WebRTCReceiver) rembLoop(cycle int) {
	defer v.wg.Done()
	t := time.NewTicker(time.Duration(cycle) * time.Second)
	for {
		select {
		case <-t.C:
			// only calc video recently
			v.lostRate, v.bandwidth = v.buffer.GetLostRateBandwidth(uint64(cycle))
			var bw uint64
			switch {
			case v.lostRate == 0 && v.bandwidth == 0:
				bw = v.maxBandwidth
			case v.lostRate >= 0 && v.lostRate < 0.1:
				bw = v.bandwidth * 2
			default:
				bw = uint64(float64(v.bandwidth) * (1 - v.lostRate))
			}

			if bw > v.maxBandwidth && v.maxBandwidth > 0 {
				bw = v.maxBandwidth
			}

			remb := &rtcp.ReceiverEstimatedMaximumBitrate{
				SenderSSRC: v.buffer.GetSSRC(),
				Bitrate:    bw,
				SSRCs:      []uint32{v.buffer.GetSSRC()},
			}
			v.rtcpCh <- remb
		case <-v.ctx.Done():
			t.Stop()
			return
		}
	}
}

func (v *WebRTCReceiver) tccLoop(cycle int) {
	defer v.wg.Done()
	feedbackPacketCount := uint8(0)
	t := time.NewTicker(time.Duration(cycle) * time.Millisecond)
	for {
		select {
		case <-t.C:
			cp := len(v.rtpExtInfoChan)
			if cp == 0 {
				continue
			}

			// get all rtp extension infos from channel
			rtpExtInfo := make(map[uint16]int64)
			for i := 0; i < cp; i++ {
				info := <-v.rtpExtInfoChan
				rtpExtInfo[info.TSN] = info.Timestamp
			}

			// find the min and max transport sn
			var minTSN, maxTSN uint16
			for tsn := range rtpExtInfo {

				// init
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

			// force small deta rtcp.RunLengthChunk
			chunk := &rtcp.RunLengthChunk{
				Type:               rtcp.TypeTCCRunLengthChunk,
				PacketStatusSymbol: rtcp.TypeTCCPacketReceivedSmallDelta,
				RunLength:          maxTSN - minTSN + 1,
			}

			// gather deltas
			var recvDeltas []*rtcp.RecvDelta
			var refTime uint32
			var lastTS int64
			var baseTimeTicks int64
			for i := minTSN; i <= maxTSN; i++ {
				ts, ok := rtpExtInfo[i]

				// lost packet
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

				// received packet
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
func (v *WebRTCReceiver) stats() string {
	switch v.track.Kind() {
	case webrtc.RTPCodecTypeVideo:
		return fmt.Sprintf("payload: %d | lostRate: %.2f | bandwidth: %dkbps | %s", v.buffer.GetPayloadType(), v.lostRate, v.bandwidth/1000, v.buffer.stats())
	case webrtc.RTPCodecTypeAudio:
		return fmt.Sprintf("payload: %d", v.track.PayloadType())
	default:
		return ""
	}
}

func startVideoReceiver(v *WebRTCReceiver, wStart chan struct{}) {
	defer func() {
		close(v.rtpCh)
		close(v.rtcpCh)
		if v.onCloseHandler != nil {
			v.onCloseHandler()
		}
	}()

	pliCycle := routerConfig.Video.PLICycle
	if pliCycle == 0 {
		pliCycle = 1
	}
	v.pliCycle = pliCycle
	v.rtcpCh = make(chan rtcp.Packet, maxSize)
	v.buffer = NewBuffer(v.track.SSRC(), v.track.PayloadType(), BufferOptions{
		BufferTime: routerConfig.Video.MaxBufferTime,
	})
	v.maxBandwidth = routerConfig.MaxBandwidth * 1000

	for _, feedback := range v.track.Codec().RTCPFeedback {
		switch feedback.Type {
		case webrtc.TypeRTCPFBTransportCC:
			log.Debugf("Setting feedback %s", webrtc.TypeRTCPFBTransportCC)
			v.feedback = webrtc.TypeRTCPFBTransportCC
			v.wg.Add(1)
			go v.tccLoop(routerConfig.Video.TCCCycle)
		case webrtc.TypeRTCPFBGoogREMB:
			log.Debugf("Setting feedback %s", webrtc.TypeRTCPFBGoogREMB)
			v.feedback = webrtc.TypeRTCPFBGoogREMB
			v.wg.Add(1)
			go v.rembLoop(routerConfig.Video.REMBCycle)
		}
	}
	// Start rtcp reader from track
	v.wg.Add(1)
	go v.receiveRTP()
	// Start pli loop
	v.wg.Add(1)
	go v.pliLoop()
	// Start buffer loop
	v.wg.Add(1)
	go v.bufferRtcpLoop()
	// Receiver start loops done, send start signal
	wStart <- struct{}{}
	v.wg.Wait()
}

func startAudioReceiver(v *WebRTCReceiver, wStart chan struct{}) {
	defer func() {
		close(v.rtpCh)
		if v.onCloseHandler != nil {
			v.onCloseHandler()
		}
	}()
	v.wg.Add(1)
	go func() {
		defer v.wg.Done()
		for {
			pkt, err := v.track.ReadRTP()
			// EOF signal received, this means that the remote track has been removed
			// or the peer has been disconnected. The router must be gracefully shutdown
			if err == io.EOF {
				v.Close()
				return
			}

			if err != nil {
				log.Errorf("rtp err => %v", err)
				continue
			}

			select {
			case <-v.ctx.Done():
				return
			default:
				v.rtpCh <- pkt
			}
		}
	}()
	wStart <- struct{}{}
	v.wg.Wait()
}
