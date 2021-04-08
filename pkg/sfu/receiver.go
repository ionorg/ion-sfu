package sfu

import (
	"io"
	"math/rand"
	"sync"
	"time"

	"github.com/gammazero/workerpool"
	"github.com/pion/ion-sfu/pkg/buffer"
	"github.com/pion/ion-sfu/pkg/stats"
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
	AddUpTrack(track *webrtc.TrackRemote, buffer *buffer.Buffer, bestQualityFirst bool)
	AddDownTrack(track *DownTrack, bestQualityFirst bool)
	SwitchDownTrack(track *DownTrack, layer int) error
	GetBitrate() [3]uint64
	GetMaxTemporalLayer() [3]int64
	RetransmitPackets(track *DownTrack, packets []packetMeta) error
	DeleteDownTrack(layer int, id string)
	OnCloseHandler(fn func())
	SendRTCP(p []rtcp.Packet)
	SetRTCPCh(ch chan []rtcp.Packet)
}

// WebRTCReceiver receives a video track
type WebRTCReceiver struct {
	sync.Mutex
	rtcpMu    sync.Mutex
	closeOnce sync.Once

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
	locks          [3]sync.Mutex
	buffers        [3]*buffer.Buffer
	upTracks       [3]*webrtc.TrackRemote
	stats          [3]*stats.Stream
	downTracks     [3][]*DownTrack
	pendingTracks  [3][]*DownTrack
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

func (w *WebRTCReceiver) AddUpTrack(track *webrtc.TrackRemote, buff *buffer.Buffer, bestQualityFirst bool) {
	var layer int

	switch track.RID() {
	case fullResolution:
		layer = 2
	case halfResolution:
		layer = 1
	default:
		layer = 0
	}

	w.Lock()
	w.upTracks[layer] = track
	w.buffers[layer] = buff
	w.downTracks[layer] = make([]*DownTrack, 0, 10)
	w.Unlock()

	subBestQuality := func(targetLayer int) {
		for l := 0; l < targetLayer; l++ {
			w.locks[l].Lock()
			for _, dt := range w.downTracks[l] {
				dt.SwitchSpatialLayer(int64(targetLayer), false)
			}
			w.locks[l].Unlock()
		}
	}

	subLowestQuality := func(targetLayer int) {
		for l := 2; l != targetLayer; l-- {
			w.locks[l].Lock()
			for _, dt := range w.downTracks[l] {
				dt.SwitchSpatialLayer(int64(targetLayer), false)
			}
			w.locks[l].Unlock()
		}
	}

	if w.isSimulcast {
		if bestQualityFirst {
			if layer < 2 {
				w.locks[layer+1].Lock()
				t := w.downTracks[layer+1]
				w.locks[layer+1].Unlock()
				if t == nil {
					subBestQuality(layer)
				}
			} else {
				subBestQuality(layer)
			}
		} else {
			if layer > 0 {
				w.locks[layer-1].Lock()
				t := w.downTracks[layer-1]
				w.locks[layer-1].Unlock()
				if t == nil {
					subLowestQuality(layer)
				}
			} else {
				subLowestQuality(layer)
			}
		}
	}
	go w.writeRTP(layer)

}

func (w *WebRTCReceiver) AddDownTrack(track *DownTrack, bestQualityFirst bool) {
	layer := 0
	if w.isSimulcast {
		w.Lock()
		for i, t := range w.upTracks {
			if t != nil {
				layer = i
				if !bestQualityFirst {
					break
				}
			}
		}
		w.Unlock()
		w.locks[layer].Lock()
		if downTrackSubscribed(w.downTracks[layer], track) {
			w.locks[layer].Unlock()
			return
		}
		track.SetInitialLayers(int64(layer), 2)
		track.maxSpatialLayer = 2
		track.maxTemporalLayer = 2
		track.lastSSRC = w.SSRC(layer)
		track.trackType = SimulcastDownTrack
		track.payload = packetFactory.Get().([]byte)
	} else {
		w.locks[layer].Lock()
		if downTrackSubscribed(w.downTracks[layer], track) {
			w.locks[layer].Unlock()
			return
		}
		track.SetInitialLayers(0, 0)
		track.trackType = SimpleDownTrack
	}

	w.downTracks[layer] = append(w.downTracks[layer], track)
	w.locks[layer].Unlock()
}

func (w *WebRTCReceiver) SwitchDownTrack(track *DownTrack, layer int) error {
	if buf := w.buffers[layer]; buf != nil {
		w.locks[layer].Lock()
		w.pendingTracks[layer] = append(w.pendingTracks[layer], track)
		w.locks[layer].Unlock()
		return nil
	}
	return errNoReceiverFound
}

func (w *WebRTCReceiver) GetBitrate() [3]uint64 {
	var br [3]uint64
	for i, buff := range w.buffers {
		if buff != nil {
			br[i] = buff.Bitrate()
		}
	}
	return br
}

func (w *WebRTCReceiver) GetMaxTemporalLayer() [3]int64 {
	var tls [3]int64
	for i, buff := range w.buffers {
		if buff != nil {
			tls[i] = buff.MaxTemporalLayer()
		}
	}
	return tls
}

// OnCloseHandler method to be called on remote tracked removed
func (w *WebRTCReceiver) OnCloseHandler(fn func()) {
	w.onCloseHandler = fn
}

// DeleteDownTrack removes a DownTrack from a Receiver
func (w *WebRTCReceiver) DeleteDownTrack(layer int, id string) {
	w.locks[layer].Lock()
	idx := -1
	for i, dt := range w.downTracks[layer] {
		if dt.peerID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		w.locks[layer].Unlock()
		return
	}
	w.downTracks[layer][idx] = w.downTracks[layer][len(w.downTracks[layer])-1]
	w.downTracks[layer][len(w.downTracks[layer])-1] = nil
	w.downTracks[layer] = w.downTracks[layer][:len(w.downTracks[layer])-1]
	w.locks[layer].Unlock()
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

func (w *WebRTCReceiver) RetransmitPackets(track *DownTrack, packets []packetMeta) error {
	if w.nackWorker.Stopped() {
		return io.ErrClosedPipe
	}
	w.nackWorker.Submit(func() {
		for _, meta := range packets {
			pktBuff := packetFactory.Get().([]byte)
			buff := w.buffers[meta.layer]
			if buff == nil {
				break
			}
			i, err := buff.GetPacket(pktBuff, meta.sourceSeqNo)
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
			pkt.Header.SequenceNumber = meta.targetSeqNo
			pkt.Header.Timestamp = meta.timestamp
			pkt.Header.SSRC = track.ssrc
			pkt.Header.PayloadType = track.payloadType
			if track.simulcast.temporalSupported {
				switch track.mime {
				case "video/vp8":
					var vp8 buffer.VP8
					if err = vp8.Unmarshal(pkt.Payload); err != nil {
						continue
					}
					tlzoID, picID := meta.getVP8PayloadMeta()
					modifyVP8TemporalPayload(pkt.Payload, vp8.PicIDIdx, vp8.TlzIdx, picID, tlzoID, vp8.MBit)
				}
			}

			if _, err = track.writeStream.WriteRTP(&pkt.Header, pkt.Payload); err != nil {
				Logger.Error(err, "Writing rtx packet err")
			} else {
				track.UpdateStats(uint32(i))
			}

			packetFactory.Put(pktBuff)
		}
	})
	return nil
}

func (w *WebRTCReceiver) writeRTP(layer int) {
	defer func() {
		w.closeOnce.Do(func() {
			go w.closeTracks()
		})
	}()

	pli := []rtcp.Packet{
		&rtcp.PictureLossIndication{SenderSSRC: rand.Uint32(), MediaSSRC: w.SSRC(layer)},
	}
	var del []int

	for {
		pkt, err := w.buffers[layer].ReadExtended()
		if err == io.EOF {
			return
		}

		w.locks[layer].Lock()

		if w.isSimulcast && len(w.pendingTracks[layer]) > 0 {
			if pkt.KeyFrame {
				w.downTracks[layer] = append(w.downTracks[layer], w.pendingTracks[layer]...)
				w.pendingTracks[layer] = w.pendingTracks[layer][:0]
			} else {
				w.SendRTCP(pli)
			}
		}

		for idx, dt := range w.downTracks[layer] {
			if err := dt.WriteRTP(pkt); err == io.EOF || err == io.ErrClosedPipe {
				del = append(del, idx)
			}
		}
		if len(del) > 0 {
			for i := len(del) - 1; i >= 0; i-- {
				w.downTracks[layer][del[i]] = w.downTracks[layer][len(w.downTracks[layer])-1]
				w.downTracks[layer][len(w.downTracks[layer])-1] = nil
				w.downTracks[layer] = w.downTracks[layer][:len(w.downTracks[layer])-1]
			}
			del = del[:0]
		}
		w.locks[layer].Unlock()
	}

}

// closeTracks close all tracks from Receiver
func (w *WebRTCReceiver) closeTracks() {
	for idx, layer := range w.downTracks {
		w.locks[idx].Lock()
		for _, dt := range layer {
			dt.Close()
		}
		w.downTracks[idx] = w.downTracks[idx][:0]
		w.locks[idx].Unlock()
	}
	w.nackWorker.Stop()
	if w.onCloseHandler != nil {
		w.onCloseHandler()
	}
}

func downTrackSubscribed(dts []*DownTrack, dt *DownTrack) bool {
	for _, cdt := range dts {
		if cdt == dt {
			return true
		}
	}
	return false
}
