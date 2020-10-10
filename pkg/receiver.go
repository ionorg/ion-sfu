package sfu

//go:generate go run github.com/matryer/moq -out receiver_mock_test.generated.go . Receiver

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
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

// Receiver defines a interface for a track receivers
type Receiver interface {
	Track() *webrtc.Track
	ReadRTCP() chan rtcp.Packet
	WriteRTCP(rtcp.Packet) error
	AddSender(sender Sender)
	DeleteSender(pid string)
	SpatialLayer() uint8
	OnCloseHandler(fn func())
	WriteBufferedPacket(sn uint16, track *webrtc.Track, snOffset uint16, tsOffset, ssrc uint32) error
	Close()
}

// WebRTCReceiver receives a video track
type WebRTCReceiver struct {
	sync.RWMutex
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
	senders        map[string]Sender

	spatialLayer uint8

	maxBandwidth uint64
	maxNackTime  int64
	lastNack     int64
	feedback     string
	wg           sync.WaitGroup
}

// WebRTCVideoReceiverConfig .
type WebRTCVideoReceiverConfig struct {
	REMBCycle       int `mapstructure:"rembcycle"`
	TCCCycle        int `mapstructure:"tcccycle"`
	MaxBufferTime   int `mapstructure:"maxbuffertime"`
	ReceiveRTPCycle int `mapstructure:"rtpcycle"`
}

// NewWebRTCReceiver creates a new webrtc track receivers
func NewWebRTCReceiver(ctx context.Context, track *webrtc.Track, config RouterConfig) Receiver {
	ctx, cancel := context.WithCancel(ctx)

	w := &WebRTCReceiver{
		ctx:            ctx,
		cancel:         cancel,
		track:          track,
		senders:        make(map[string]Sender),
		rtpCh:          make(chan *rtp.Packet, maxSize),
		rtpExtInfoChan: make(chan rtpExtInfo, maxSize),
		lastNack:       time.Now().Unix(),
		maxNackTime:    config.MaxNackTime,
	}

	switch w.track.RID() {
	case quarterResolution:
		w.spatialLayer = 1
	case halfResolution:
		w.spatialLayer = 2
	case fullResolution:
		w.spatialLayer = 3
	default:
		w.spatialLayer = 0
	}

	waitStart := make(chan struct{})
	switch track.Kind() {
	case webrtc.RTPCodecTypeVideo:
		go startVideoReceiver(w, waitStart, config)
	case webrtc.RTPCodecTypeAudio:
		go startAudioReceiver(w, waitStart)
	}
	<-waitStart
	return w
}

// OnCloseHandler method to be called on remote tracked removed
func (w *WebRTCReceiver) OnCloseHandler(fn func()) {
	w.onCloseHandler = fn
}

func (w *WebRTCReceiver) AddSender(sender Sender) {
	w.Lock()
	defer w.Unlock()
	w.senders[sender.ID()] = sender
}

//DeleteSender removes a Sender from a Receiver
func (w *WebRTCReceiver) DeleteSender(pid string) {
	w.Lock()
	defer w.Unlock()
	delete(w.senders, pid)
}

func (w *WebRTCReceiver) SpatialLayer() uint8 {
	return w.spatialLayer
}

//closeSenders Close all senders from Receiver
func (w *WebRTCReceiver) closeSenders() {
	w.RLock()
	defer w.RUnlock()
	for _, sender := range w.senders {
		sender.Close()
	}
}

// ReadRTCP read rtcp packets
func (w *WebRTCReceiver) ReadRTCP() chan rtcp.Packet {
	return w.rtcpCh
}

// WriteRTCP write rtcp packet
func (w *WebRTCReceiver) WriteRTCP(pkt rtcp.Packet) error {
	if w.ctx.Err() != nil || w.rtcpCh == nil {
		return io.ErrClosedPipe
	}
	if _, ok := pkt.(*rtcp.TransportLayerNack); ok && w.maxNackTime > 0 {
		ln := atomic.LoadInt64(&w.lastNack)
		if (time.Now().Unix() - ln) < w.maxNackTime {
			return nil
		}
		atomic.StoreInt64(&w.lastNack, time.Now().Unix())
	}
	w.rtcpCh <- pkt
	return nil
}

// Track returns receivers track
func (w *WebRTCReceiver) Track() *webrtc.Track {
	return w.track
}

// WriteBufferedPacket writes buffered packet to track, return error if packet not found
func (w *WebRTCReceiver) WriteBufferedPacket(sn uint16, track *webrtc.Track, snOffset uint16, tsOffset, ssrc uint32) error {
	if w.buffer == nil || w.ctx.Err() != nil {
		return nil
	}
	return w.buffer.WritePacket(sn, track, snOffset, tsOffset, ssrc)
}

// Close gracefully close the track
func (w *WebRTCReceiver) Close() {
	if w.ctx.Err() != nil {
		return
	}
	w.cancel()
}

// receiveRTP receive all incoming tracks' rtp and sent to one channel
func (w *WebRTCReceiver) receiveRTP() {
	defer w.wg.Done()
	for {
		pkt, err := w.track.ReadRTP()
		// EOF signal received, this means that the remote track has been removed
		// or the peer has been disconnected. The router must be gracefully shutdown,
		// waiting for all the receivers routines to stop.
		if err == io.EOF {
			w.Close()
			return
		}

		if err != nil {
			log.Errorf("rtp err => %v", err)
			continue
		}

		w.buffer.Push(pkt)

		if w.feedback == webrtc.TypeRTCPFBTransportCC {
			// store arrival time
			timestampUs := time.Now().UnixNano() / 1000
			rtpTCC := rtp.TransportCCExtension{}
			err = rtpTCC.Unmarshal(pkt.GetExtension(tccExtMapID))
			if err == nil {
				// if time.Now().Sub(b.bufferStartTS) > time.Second {

				// only calc the packet which rtpTCC.TransportSequence > b.lastTCCSN
				// https://webrtc.googlesource.com/src/webrtc/+/f54860e9ef0b68e182a01edc994626d21961bc4b/modules/rtp_rtcp/source/rtcp_packet/transport_feedback.cc#353
				// if rtpTCC.TransportSequence > b.lastTCCSN {
				w.rtpExtInfoChan <- rtpExtInfo{
					TSN:       rtpTCC.TransportSequence,
					Timestamp: timestampUs,
				}
				// b.lastTCCSN = rtpTCC.TransportSequence
				// }
			}
		}

		select {
		case <-w.ctx.Done():
			return
		default:
			w.rtpCh <- pkt
		}
	}
}

func (w *WebRTCReceiver) fwdRTP() {
	for pkt := range w.rtpCh {
		// Push to sub send queues
		w.RLock()
		for _, sub := range w.senders {
			sub.WriteRTP(pkt)
		}
		w.RUnlock()
	}
}

func (w *WebRTCReceiver) rembLoop(cycle int) {
	defer w.wg.Done()
	if cycle <= 0 {
		cycle = 1
	}
	t := time.NewTicker(time.Duration(cycle) * time.Second)

	for {
		select {
		case <-t.C:
			// only calc video recently
			w.lostRate, w.bandwidth = w.buffer.GetLostRateBandwidth(uint64(cycle))
			var bw uint64
			switch {
			case w.lostRate == 0 && w.bandwidth == 0:
				bw = w.maxBandwidth
			case w.lostRate >= 0 && w.lostRate < 0.1:
				bw = w.bandwidth * 2
			default:
				bw = uint64(float64(w.bandwidth) * (1 - w.lostRate))
			}

			if bw > w.maxBandwidth && w.maxBandwidth > 0 {
				bw = w.maxBandwidth
			}

			remb := &rtcp.ReceiverEstimatedMaximumBitrate{
				SenderSSRC: w.buffer.GetSSRC(),
				Bitrate:    bw,
				SSRCs:      []uint32{w.buffer.GetSSRC()},
			}
			w.rtcpCh <- remb
		case <-w.ctx.Done():
			t.Stop()
			return
		}
	}
}

func (w *WebRTCReceiver) tccLoop(cycle int) {
	defer w.wg.Done()
	feedbackPacketCount := uint8(0)
	t := time.NewTicker(time.Duration(cycle) * time.Millisecond)
	for {
		select {
		case <-t.C:
			cp := len(w.rtpExtInfoChan)
			if cp == 0 {
				continue
			}

			// get all rtp extension infos from channel
			rtpExtInfo := make(map[uint16]int64)
			for i := 0; i < cp; i++ {
				info := <-w.rtpExtInfoChan
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
				// SenderSSRC:         w.ssrc,
				MediaSSRC:          w.track.SSRC(),
				BaseSequenceNumber: minTSN,
				PacketStatusCount:  maxTSN - minTSN + 1,
				ReferenceTime:      refTime,
				FbPktCount:         feedbackPacketCount,
				RecvDeltas:         recvDeltas,
				PacketChunks:       []rtcp.PacketStatusChunk{chunk},
			}
			rtcpTCC.Header.Length = rtcpTCC.Len()/4 - 1
			w.rtcpCh <- rtcpTCC
			feedbackPacketCount++
		case <-w.ctx.Done():
			t.Stop()
			return
		}
	}
}

func startVideoReceiver(w *WebRTCReceiver, wStart chan struct{}, config RouterConfig) {
	defer func() {
		w.closeSenders()
		w.buffer.Stop()
		close(w.rtpCh)
		close(w.rtcpCh)
		if w.onCloseHandler != nil {
			w.onCloseHandler()
		}
	}()

	w.rtcpCh = make(chan rtcp.Packet, maxSize)
	w.buffer = NewBuffer(w.rtcpCh, w.track.SSRC(), w.track.PayloadType(), BufferOptions{
		BufferTime: config.Video.MaxBufferTime,
	})
	w.maxBandwidth = config.MaxBandwidth * 1000

	for _, feedback := range w.track.Codec().RTCPFeedback {
		switch feedback.Type {
		case webrtc.TypeRTCPFBTransportCC:
			log.Debugf("Setting feedback %s", webrtc.TypeRTCPFBTransportCC)
			w.feedback = webrtc.TypeRTCPFBTransportCC
			w.wg.Add(1)
			go w.tccLoop(config.Video.TCCCycle)
		case webrtc.TypeRTCPFBGoogREMB:
			log.Debugf("Setting feedback %s", webrtc.TypeRTCPFBGoogREMB)
			w.feedback = webrtc.TypeRTCPFBGoogREMB
			w.wg.Add(1)
			go w.rembLoop(config.Video.REMBCycle)
		}
	}
	w.wg.Add(1)
	go w.receiveRTP()
	// Receiver start loops done, send start signal
	go w.fwdRTP()
	wStart <- struct{}{}
	w.wg.Wait()
}

func startAudioReceiver(w *WebRTCReceiver, wStart chan struct{}) {
	defer func() {
		w.closeSenders()
		close(w.rtpCh)
		if w.onCloseHandler != nil {
			w.onCloseHandler()
		}
	}()
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for {
			pkt, err := w.track.ReadRTP()
			// EOF signal received, this means that the remote track has been removed
			// or the peer has been disconnected. The router must be gracefully shutdown
			if err == io.EOF {
				w.Close()
				return
			}

			if err != nil {
				log.Errorf("rtp err => %v", err)
				continue
			}

			select {
			case <-w.ctx.Done():
				return
			default:
				w.rtpCh <- pkt
			}
		}
	}()
	go w.fwdRTP()
	wStart <- struct{}{}
	w.wg.Wait()
}
