package sfu

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
	// bandwidth range(kbps)
	// minBandwidth = 200
	maxSize = 1024
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
	receiver       *webrtc.RTPReceiver
	buffer         *Buffer
	track          *webrtc.Track
	bandwidth      uint64
	rtpCh          chan *rtp.Packet
	rtcpCh         chan rtcp.Packet
	onCloseHandler func()
	senders        map[string]Sender

	spatialLayer uint8

	wg sync.WaitGroup
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
		ctx:     ctx,
		cancel:  cancel,
		track:   track,
		senders: make(map[string]Sender),
		rtpCh:   make(chan *rtp.Packet, maxSize),
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
				w.buffer.setSR(pkt.RTPTime, pkt.NTPTime)
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
		close(w.rtcpCh)
		if w.onCloseHandler != nil {
			w.onCloseHandler()
		}
	}()

	w.rtcpCh = make(chan rtcp.Packet, maxSize)
	w.buffer = NewBuffer(w.rtcpCh, w.track, BufferOptions{
		BufferTime: config.Video.MaxBufferTime,
	})

	w.wg.Add(1)
	go w.readRTP()
	// Receiver start loops done, send start signal
	go w.fwdRTP()
	go w.readRTCP()
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
