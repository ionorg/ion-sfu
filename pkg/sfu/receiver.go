package sfu

import (
	"io"
	"sync"
	"time"

	"github.com/gammazero/workerpool"
	"github.com/pion/ion-sfu/pkg/buffer"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// Receiver defines a interface for a track receivers
type Receiver interface {
	TrackID() string
	StreamID() string
	Codec() webrtc.RTPCodecParameters
	Kind() webrtc.RTPCodecType
	SSRC(layer int) uint32
	AddUpTrack(track *webrtc.TrackRemote, buffer *buffer.Buffer)
	AddDownTrack(track *DownTrack, bestQualityFirst bool)
	SubDownTrack(track *DownTrack, layer int) error
	RetransmitPackets(track *DownTrack, packets []uint16)
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
	buffers        [3]*buffer.Buffer
	upTracks       [3]*webrtc.TrackRemote
	downTracks     [3][]*DownTrack
	nackWorker     *workerpool.WorkerPool
	isSimulcast    bool
	onCloseHandler func()
}

// NewWebRTCReceiver creates a new webrtc track receivers
func NewWebRTCReceiver(receiver *webrtc.RTPReceiver, track *webrtc.TrackRemote, pid string) Receiver {
	return &WebRTCReceiver{
		peerID:      pid,
		receiver:    receiver,
		trackID:     track.ID(),
		streamID:    track.StreamID(),
		codec:       track.Codec(),
		kind:        track.Kind(),
		nackWorker:  workerpool.New(1),
		isSimulcast: len(track.RID()) > 0,
	}
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

func (w *WebRTCReceiver) AddUpTrack(track *webrtc.TrackRemote, buff *buffer.Buffer) {
	var layer int

	switch track.RID() {
	case fullResolution:
		layer = 2
	case halfResolution:
		layer = 1
	default:
		layer = 0
	}

	w.upTracks[layer] = track
	w.buffers[layer] = buff
	w.downTracks[layer] = make([]*DownTrack, 0, 10)
	go w.writeRTP(layer)
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
		track.simulcast.targetSpatialLayer = layer
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
		w.downTracks[layer] = append(dts, track)
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
	if idx == -1 {
		w.Unlock()
		return
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

func (w *WebRTCReceiver) RetransmitPackets(track *DownTrack, packets []uint16) {
	w.nackWorker.Submit(func() {
		pktBuff := packetFactory.Get().([]byte)
		for _, sn := range packets {
			i, err := w.buffers[track.currentSpatialLayer].GetPacket(pktBuff, sn)
			if err != nil {
				if err == io.EOF {
					break
				}
				continue
			}
			var pkt rtp.Packet
			if err = pkt.Unmarshal(pktBuff[:i]); err != nil {
				continue
			}
			if err = track.WriteRTP(pkt); err == io.EOF {
				break
			}
		}
		packetFactory.Put(pktBuff)
	})
}

func (w *WebRTCReceiver) writeRTP(layer int) {
	defer func() {
		w.closeTracks(layer)
		w.nackWorker.Stop()
		if w.onCloseHandler != nil {
			w.onCloseHandler()
		}
	}()
	for pkt := range w.buffers[layer].PacketChan() {
		w.Lock()
		for _, dt := range w.downTracks[layer] {
			if err := dt.WriteRTP(pkt); err == io.EOF {
				go w.DeleteDownTrack(layer, dt.id)
			}
		}
		w.Unlock()
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
