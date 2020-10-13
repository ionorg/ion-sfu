package sfu

//go:generate go run github.com/matryer/moq -out receiver_mock_test.generated.go . Receiver

import (
	"context"
	"io"
	"sync"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

const (
	maxSize = 1024
)

// Receiver defines a interface for a track receivers
type Receiver interface {
	Track() *webrtc.Track
	AddSender(sender Sender)
	DeleteSender(pid string)
	SpatialLayer() uint8
	GetRTCP() []rtcp.Packet
	OnCloseHandler(fn func())
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
	senders        map[string]Sender
	onCloseHandler func()

	spatialLayer uint8

	wg sync.WaitGroup
}

// NewWebRTCReceiver creates a new webrtc track receivers
func NewWebRTCReceiver(ctx context.Context, receiver *webrtc.RTPReceiver, track *webrtc.Track, config RouterConfig) Receiver {
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
		w.spatialLayer = 1
	case halfResolution:
		w.spatialLayer = 2
	case fullResolution:
		w.spatialLayer = 3
	default:
		w.spatialLayer = 0
	}

	waitStart := make(chan struct{})
	switch w.track.Kind() {
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

// DeleteSender removes a Sender from a Receiver
func (w *WebRTCReceiver) DeleteSender(pid string) {
	w.Lock()
	defer w.Unlock()
	delete(w.senders, pid)
}

func (w *WebRTCReceiver) SpatialLayer() uint8 {
	return w.spatialLayer
}

// closeSenders close all senders from Receiver
func (w *WebRTCReceiver) closeSenders() {
	w.RLock()
	defer w.RUnlock()
	for _, sender := range w.senders {
		sender.Close()
	}
}

// Track returns receivers track
func (w *WebRTCReceiver) Track() *webrtc.Track {
	return w.track
}

func (w *WebRTCReceiver) GetRTCP() []rtcp.Packet {
	return w.buffer.getRTCP()
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

// readRTP receive all incoming tracks' rtp and sent to one channel
func (w *WebRTCReceiver) readRTP() {
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

		select {
		case <-w.ctx.Done():
			return
		default:
			w.rtpCh <- pkt
		}
	}
}

func (w *WebRTCReceiver) readRTCP() {
	defer w.wg.Done()
	for {
		pkts, err := w.receiver.ReadRTCP()
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

func startVideoReceiver(w *WebRTCReceiver, wStart chan struct{}, config RouterConfig) {
	defer func() {
		w.closeSenders()
		close(w.rtpCh)
		if w.onCloseHandler != nil {
			w.onCloseHandler()
		}
	}()

	w.buffer = NewBuffer(w.track, BufferOptions{
		BufferTime: config.MaxBufferTime,
	})

	w.wg.Add(1)
	go w.readRTP()
	w.wg.Add(1)
	go w.readRTCP()
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
