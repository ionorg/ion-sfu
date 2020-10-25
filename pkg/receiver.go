package sfu

//go:generate go run github.com/matryer/moq -out receiver_mock_test.generated.go . Receiver

import (
	"context"
	"io"
	"sync"

	log "github.com/pion/ion-log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

const (
	maxSize = 1024
)

// Receiver defines a interface for a track receivers
type Receiver interface {
	Start()
	Track() *webrtc.Track
	AddSender(sender Sender)
	DeleteSender(pid string)
	SpatialLayer() uint8
	OnCloseHandler(fn func())
	OnTransportWideCC(fn func(sn uint16, timeNS int64, marker bool))
	SendRTCP(p []rtcp.Packet)
	SetRTCPCh(ch chan []rtcp.Packet)
	WriteBufferedPacket(sn uint16, track *webrtc.Track, snOffset uint16, tsOffset, ssrc uint32) error
	Close()
}

// WebRTCReceiver receives a video track
type WebRTCReceiver struct {
	sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	receiver       *webrtc.RTPReceiver
	track          *webrtc.Track
	buffer         *Buffer
	bandwidth      uint64
	rtpCh          chan *rtp.Packet
	rtcpCh         chan []rtcp.Packet
	senders        map[string]Sender
	onCloseHandler func()

	spatialLayer uint8
}

// NewWebRTCReceiver creates a new webrtc track receivers
func NewWebRTCReceiver(ctx context.Context, receiver *webrtc.RTPReceiver, track *webrtc.Track, config BufferOptions) Receiver {
	ctx, cancel := context.WithCancel(ctx)

	w := &WebRTCReceiver{
		ctx:      ctx,
		cancel:   cancel,
		receiver: receiver,
		track:    track,
		senders:  make(map[string]Sender),
		rtpCh:    make(chan *rtp.Packet, maxSize),
	}

	switch w.track.RID() {
	case quarterResolution:
		w.spatialLayer = 0
	case halfResolution:
		w.spatialLayer = 1
	case fullResolution:
		w.spatialLayer = 2
	default:
		w.spatialLayer = 0
	}

	w.buffer = NewBuffer(track, config)

	w.buffer.onFeedback(func(packets []rtcp.Packet) {
		w.rtcpCh <- packets
	})

	w.buffer.onLostHandler(func(nack *rtcp.TransportLayerNack) {
		log.Debugf("Writing nack to mediaSSRC: %d, missing sn: %d, bitmap: %b", track.SSRC(), nack.Nacks[0].PacketID, nack.Nacks[0].LostPackets)
		w.rtcpCh <- []rtcp.Packet{nack}
	})

	return w
}

func (w *WebRTCReceiver) Start() {
	go w.readRTP()
	if len(w.track.RID()) > 0 {
		go w.readSimulcastRTCP(w.track.RID())
	} else {
		go w.readRTCP()
	}
	go w.writeRTP()
}

// OnCloseHandler method to be called on remote tracked removed
func (w *WebRTCReceiver) OnCloseHandler(fn func()) {
	w.onCloseHandler = fn
}

func (w *WebRTCReceiver) OnTransportWideCC(fn func(sn uint16, timeNS int64, marker bool)) {
	w.buffer.onTransportWideCC(fn)
}

func (w *WebRTCReceiver) AddSender(sender Sender) {
	w.Lock()
	defer w.Unlock()
	w.senders[sender.ID()] = sender
}

// DeleteSender removes a Sender from a Receiver
func (w *WebRTCReceiver) DeleteSender(pid string) {
	w.Lock()
	defer w.Unlock()
	delete(w.senders, pid)
}

func (w *WebRTCReceiver) SendRTCP(p []rtcp.Packet) {
	w.rtcpCh <- p
}

func (w *WebRTCReceiver) SpatialLayer() uint8 {
	return w.spatialLayer
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

func (w *WebRTCReceiver) SetRTCPCh(ch chan []rtcp.Packet) {
	w.rtcpCh = ch
}

// Close gracefully close the track
func (w *WebRTCReceiver) Close() {
	if w.ctx.Err() != nil {
		return
	}
	w.cancel()
}

// readRTP receive all incoming tracks' rtp and sent to one channel
func (w *WebRTCReceiver) readRTP() {
	defer func() {
		w.closeSenders()
		close(w.rtpCh)
		if w.onCloseHandler != nil {
			w.onCloseHandler()
		}
	}()
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

		w.buffer.push(pkt)

		select {
		case <-w.ctx.Done():
			return
		default:
			w.rtpCh <- pkt
		}
	}
}

func (w *WebRTCReceiver) readRTCP() {
	for {
		pkts, err := w.receiver.ReadRTCP()
		if err == io.ErrClosedPipe || err == io.EOF || w.ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Errorf("rtcp err => %v", err)
			continue
		}

		for _, pkt := range pkts {
			switch pkt := pkt.(type) {
			case *rtcp.SenderReport:
				w.buffer.setSenderReportData(pkt.RTPTime, pkt.NTPTime)
			}
		}
	}
}

func (w *WebRTCReceiver) readSimulcastRTCP(rid string) {
	for {
		pkts, err := w.receiver.ReadSimulcastRTCP(rid)
		if err == io.ErrClosedPipe || w.ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Errorf("rtcp err => %v", err)
			continue
		}

		for _, pkt := range pkts {
			switch pkt := pkt.(type) {
			case *rtcp.SenderReport:
				w.buffer.setSenderReportData(pkt.RTPTime, pkt.NTPTime)
			}
		}
	}
}

func (w *WebRTCReceiver) writeRTP() {
	for pkt := range w.rtpCh {
		w.RLock()
		for _, sub := range w.senders {
			sub.WriteRTP(pkt)
		}
		w.RUnlock()
	}
}

// closeSenders close all senders from Receiver
func (w *WebRTCReceiver) closeSenders() {
	w.RLock()
	defer w.RUnlock()
	for _, sender := range w.senders {
		sender.Close()
	}
}
