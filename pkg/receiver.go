package sfu

//go:generate go run github.com/matryer/moq -out receiver_mock_test.generated.go . Receiver

import (
	"io"
	"sync"
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
	Track() *webrtc.TrackRemote
	AddDownTrack(track *DownTrack)
	DeleteDownTrack(id string)
	SpatialLayer() uint8
	OnCloseHandler(fn func())
	OnTransportWideCC(fn func(sn uint16, timeNS int64, marker bool))
	SendRTCP(p []rtcp.Packet)
	SetRTCPCh(ch chan []rtcp.Packet)
	WriteBufferedPacket(track *DownTrack, sn []uint16) error
}

// WebRTCReceiver receives a video track
type WebRTCReceiver struct {
	sync.RWMutex

	rtcpMu         sync.RWMutex
	receiver       *webrtc.RTPReceiver
	track          *webrtc.TrackRemote
	buffer         *Buffer
	bandwidth      uint64
	lastPli        int64
	rtpCh          chan *rtp.Packet
	rtcpCh         chan []rtcp.Packet
	downTracks     map[string]*DownTrack
	onCloseHandler func()

	spatialLayer uint8
}

// NewWebRTCReceiver creates a new webrtc track receivers
func NewWebRTCReceiver(receiver *webrtc.RTPReceiver, track *webrtc.TrackRemote, config BufferOptions) Receiver {
	w := &WebRTCReceiver{
		receiver:   receiver,
		track:      track,
		downTracks: make(map[string]*DownTrack),
		rtpCh:      make(chan *rtp.Packet, maxSize),
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

func (w *WebRTCReceiver) AddDownTrack(track *DownTrack) {
	w.Lock()
	w.downTracks[track.peerID] = track
	w.Unlock()
}

// DeleteDownTrack removes a DownTrack from a Receiver
func (w *WebRTCReceiver) DeleteDownTrack(id string) {
	w.Lock()
	delete(w.downTracks, id)
	w.Unlock()
}

func (w *WebRTCReceiver) SendRTCP(p []rtcp.Packet) {
	if _, ok := p[0].(*rtcp.PictureLossIndication); ok {
		w.rtcpMu.Lock()
		defer w.rtcpMu.Unlock()
		if time.Now().UnixNano()-w.lastPli < 500e6 {
			return
		}
		w.lastPli = time.Now().UnixNano()
	}

	w.rtcpCh <- p
}

func (w *WebRTCReceiver) SpatialLayer() uint8 {
	return w.spatialLayer
}

// Track returns receivers track
func (w *WebRTCReceiver) Track() *webrtc.TrackRemote {
	return w.track
}

// WriteBufferedPacket writes buffered packet to track, return error if packet not found
func (w *WebRTCReceiver) WriteBufferedPacket(track *DownTrack, sn []uint16) error {
	if w.buffer == nil {
		return nil
	}
	for _, seq := range sn {
		h, p, err := w.buffer.getPacket(seq + track.snOffset)
		if err != nil {
			continue
		}
		h.SequenceNumber -= track.snOffset
		h.Timestamp -= track.tsOffset
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
		w.closeTracks()
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
			log.Debugf("receiver %d read rtp eof", w.track.SSRC())
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
			log.Debugf("receiver %d readrtcp eof", w.track.SSRC())
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
		for _, dt := range w.downTracks {
			if err := dt.WriteRTP(pkt); err == io.EOF {
				delete(w.downTracks, dt.peerID)
			}
		}
		w.RUnlock()
	}
}

// closeTracks close all tracks from Receiver
func (w *WebRTCReceiver) closeTracks() {
	w.RLock()
	defer w.RUnlock()
	for _, dt := range w.downTracks {
		dt.Close()
	}
}
