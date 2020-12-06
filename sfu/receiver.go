package sfu

//go:generate go run github.com/matryer/moq -out receiver_mock_test.generated.go . Receiver

import (
	"io"
	"sync"
	"time"

	log "github.com/pion/ion-log"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

const (
	maxSize = 1024
)

// Receiver defines a interface for a track receivers
type Receiver interface {
	TrackID() string
	StreamID() string
	Codec() webrtc.RTPCodecParameters
	Kind() webrtc.RTPCodecType
	SSRC(layer int) uint32
	AddUpTrack(track *webrtc.TrackRemote)
	AddDownTrack(track *DownTrack, bestQualityFirst bool)
	SubDownTrack(track *DownTrack, layer int) error
	DeleteDownTrack(layer int, id string)
	OnCloseHandler(fn func())
	SendRTCP(p []rtcp.Packet)
	SetRTCPCh(ch chan []rtcp.Packet)
}

// WebRTCReceiver receives a video track
type WebRTCReceiver struct {
	sync.Mutex
	rtcpMu sync.RWMutex

	peerID         string
	trackID        string
	streamID       string
	kind           webrtc.RTPCodecType
	bandwidth      uint64
	lastPli        int64
	stream         string
	receiver       *webrtc.RTPReceiver
	codec          webrtc.RTPCodecParameters
	rtcpCh         chan []rtcp.Packet
	upTracks       [3]*webrtc.TrackRemote
	downTracks     [3][]*DownTrack
	isSimulcast    bool
	onCloseHandler func()
}

// NewWebRTCReceiver creates a new webrtc track receivers
func NewWebRTCReceiver(receiver *webrtc.RTPReceiver, track *webrtc.TrackRemote, pid string) Receiver {
	w := &WebRTCReceiver{
		peerID:      pid,
		receiver:    receiver,
		trackID:     track.ID(),
		streamID:    track.StreamID(),
		codec:       track.Codec(),
		kind:        track.Kind(),
		isSimulcast: len(track.RID()) > 0,
	}

	return w
}

func (w *WebRTCReceiver) StreamID() string {
	return w.streamID
}

func (w *WebRTCReceiver) TrackID() string {
	return w.trackID
}

func (w *WebRTCReceiver) SSRC(layer int) uint32 {
	if track := w.upTracks[layer]; track != nil {
		return uint32(track.SSRC())
	}
	return 0
}

func (w *WebRTCReceiver) Codec() webrtc.RTPCodecParameters {
	return w.codec
}

func (w *WebRTCReceiver) Kind() webrtc.RTPCodecType {
	return w.kind
}

func (w *WebRTCReceiver) AddUpTrack(track *webrtc.TrackRemote) {
	var layer int

	switch track.RID() {
	case fullResolution:
		layer = 2
		w.upTracks[layer] = track
	case halfResolution:
		layer = 1
		w.upTracks[layer] = track
	default:
		layer = 0
		w.upTracks[layer] = track
	}

	w.downTracks[layer] = make([]*DownTrack, 0, 10)

	go w.readRTP(track, layer)
	if w.isSimulcast {
		go w.readSimulcastRTCP(track.RID())
	} else {
		go w.readRTCP()
	}
}

func (w *WebRTCReceiver) AddDownTrack(track *DownTrack, bestQualityFirst bool) {
	layer := 0
	if w.isSimulcast {
		for i, t := range w.upTracks {
			if t != nil {
				layer = i
				if !bestQualityFirst {
					break
				}
			}
		}
		track.currentSpatialLayer = layer
		track.trackType = SimulcastDownTrack
	} else {
		track.trackType = SimpleDownTrack
	}

	w.Lock()
	w.downTracks[layer] = append(w.downTracks[layer], track)
	w.Unlock()
}

func (w *WebRTCReceiver) SubDownTrack(track *DownTrack, layer int) error {
	w.Lock()
	if dts := w.downTracks[layer]; dts != nil {
		dts = append(dts, track)
	} else {
		w.Unlock()
		return errNoReceiverFound
	}
	w.Unlock()
	return nil
}

// OnCloseHandler method to be called on remote tracked removed
func (w *WebRTCReceiver) OnCloseHandler(fn func()) {
	w.onCloseHandler = fn
}

// DeleteDownTrack removes a DownTrack from a Receiver
func (w *WebRTCReceiver) DeleteDownTrack(layer int, id string) {
	w.Lock()
	idx := -1
	for i, dt := range w.downTracks[layer] {
		if dt.peerID == id {
			idx = i
			break
		}
	}
	w.downTracks[layer][idx] = w.downTracks[layer][len(w.downTracks[layer])-1]
	w.downTracks[layer][len(w.downTracks[layer])-1] = nil
	w.downTracks[layer] = w.downTracks[layer][:len(w.downTracks[layer])-1]
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

func (w *WebRTCReceiver) SetRTCPCh(ch chan []rtcp.Packet) {
	w.rtcpCh = ch
}

// readRTP receive all incoming tracks' rtp and sent to one channel
func (w *WebRTCReceiver) readRTP(track *webrtc.TrackRemote, layer int) {
	defer func() {
		w.closeTracks(layer)
		if w.onCloseHandler != nil {
			w.onCloseHandler()
		}
	}()
	for {
		pkt, err := track.ReadRTP()
		// EOF signal received, this means that the remote track has been removed
		// or the peer has been disconnected. The router must be gracefully shutdown,
		// waiting for all the receivers routines to stop.
		if err == io.EOF {
			log.Debugf("receiver %d read rtp eof", track.SSRC())
			return
		}

		if err != nil {
			log.Errorf("rtp err => %v", err)
			continue
		}

		w.Lock()
		i := 0
		for _, dt := range w.downTracks[layer] {
			if err := dt.WriteRTP(pkt); err != io.EOF {
				w.downTracks[layer][i] = dt
				i++
			}
		}
		for j := i; j < len(w.downTracks[layer]); j++ {
			w.downTracks[layer][j] = nil
		}
		w.downTracks[layer] = w.downTracks[layer][:i]
		w.Unlock()
	}
}

func (w *WebRTCReceiver) readRTCP() {
	for {
		_, err := w.receiver.ReadRTCP()
		if err == io.ErrClosedPipe || err == io.EOF {
			log.Debugf("receiver %s readrtcp eof", w.peerID)
			return
		}
		if err != nil {
			log.Errorf("rtcp err => %v", err)
			continue
		}
	}
}

func (w *WebRTCReceiver) readSimulcastRTCP(rid string) {
	for {
		_, err := w.receiver.ReadSimulcastRTCP(rid)
		if err == io.ErrClosedPipe || err == io.EOF {
			return
		}
		if err != nil {
			log.Errorf("rtcp err => %v", err)
			continue
		}
	}
}

// closeTracks close all tracks from Receiver
func (w *WebRTCReceiver) closeTracks(layer int) {
	w.Lock()
	defer w.Unlock()
	for _, dt := range w.downTracks[layer] {
		dt.Close()
	}
}
