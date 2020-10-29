package sfu

//go:generate go run github.com/matryer/moq -out receiver_mock_test.generated.go . Receiver

import (
	"io"
	"sync"
	"sync/atomic"
	"time"

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
	WriteBufferedPacket(sn []uint16, track *webrtc.Track, snOffset uint16, tsOffset, ssrc uint32) error
}

// WebRTCReceiver receives a video track
type WebRTCReceiver struct {
	sync.RWMutex
	receiver       *webrtc.RTPReceiver
	track          *webrtc.Track
	buffer         *Buffer
	bandwidth      uint64
	lastPli        int64
	rtpCh          chan *rtp.Packet
	rtcpCh         chan []rtcp.Packet
	senders        map[string]Sender
	onCloseHandler func()

	spatialLayer uint8
}

// NewWebRTCReceiver creates a new webrtc track receivers
func NewWebRTCReceiver(receiver *webrtc.RTPReceiver, track *webrtc.Track, config BufferOptions) Receiver {
	w := &WebRTCReceiver{
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
	w.senders[sender.ID()] = sender
	w.Unlock()
}

// DeleteSender removes a Sender from a Receiver
func (w *WebRTCReceiver) DeleteSender(pid string) {
	w.Lock()
	delete(w.senders, pid)
	w.Unlock()
}

func (w *WebRTCReceiver) SendRTCP(p []rtcp.Packet) {
	lastPli := atomic.LoadInt64(&w.lastPli)
	if _, ok := p[0].(*rtcp.PictureLossIndication); ok {
		if time.Now().UnixNano()-lastPli < 500e6 {
			return
		}
		atomic.StoreInt64(&w.lastPli, time.Now().UnixNano())
	}
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
func (w *WebRTCReceiver) WriteBufferedPacket(sn []uint16, track *webrtc.Track, snOffset uint16, tsOffset, ssrc uint32) error {
	if w.buffer == nil {
		return nil
	}
	for _, seq := range sn {
		h, p, err := w.buffer.getPacket(seq + snOffset)
		if err != nil {
			continue
		}
		h.PayloadType = track.PayloadType()
		h.SequenceNumber -= snOffset
		h.Timestamp -= tsOffset
		h.SSRC = ssrc
		if err := track.WriteRTP(&rtp.Packet{
			Header:  h,
			Payload: p,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *WebRTCReceiver) SetRTCPCh(ch chan []rtcp.Packet) {
	w.rtcpCh = ch
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
			return
		}

		if err != nil {
			log.Errorf("rtp err => %v", err)
			continue
		}

		w.buffer.push(pkt)

		w.rtpCh <- pkt
	}
}

func (w *WebRTCReceiver) readRTCP() {
	for {
		pkts, err := w.receiver.ReadRTCP()
		if err == io.ErrClosedPipe || err == io.EOF {
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
		if err == io.ErrClosedPipe || err == io.EOF {
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
