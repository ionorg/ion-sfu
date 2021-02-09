package sfu

import (
	"io"
	"sync"
	"time"

	log "github.com/pion/ion-log"

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
	AddUpTrack(track *webrtc.TrackRemote, buffer *buffer.Buffer)
	AddDownTrack(track *DownTrack, bestQualityFirst bool)
	SubDownTrack(track *DownTrack, layer int) error
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
		track.SetInitialLayers(int64(layer), 2)
		track.maxSpatialLayer = 2
		track.maxTemporalLayer = 2
		track.trackType = SimulcastDownTrack
		track.payload = packetFactory.Get().([]byte)
	} else {
		track.SetInitialLayers(0, 0)
		track.trackType = SimpleDownTrack
	}

	w.locks[layer].Lock()
	w.downTracks[layer] = append(w.downTracks[layer], track)
	w.locks[layer].Unlock()
}

func (w *WebRTCReceiver) SubDownTrack(track *DownTrack, layer int) error {
	w.locks[layer].Lock()
	if dts := w.downTracks[layer]; dts != nil {
		w.downTracks[layer] = append(dts, track)
	} else {
		w.locks[layer].Unlock()
		return errNoReceiverFound
	}
	w.locks[layer].Unlock()
	return nil
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
			buff := w.buffers[meta.getLayer()]
			if buff == nil {
				break
			}
			i, err := buff.GetPacket(pktBuff, meta.getSourceSeqNo())
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
			pkt.Header.SequenceNumber = meta.getTargetSeqNo()
			pkt.Header.Timestamp = meta.getTimestamp()
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
				log.Errorf("Writing rtx packet err: %v", err)
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
	var del []int
	for pkt := range w.buffers[layer].PacketChan() {
		w.locks[layer].Lock()
		for idx, dt := range w.downTracks[layer] {
			if err := dt.WriteRTP(pkt); err == io.EOF {
				del = append(del, idx)
			}
		}
		if len(del) > 0 {
			for _, idx := range del {
				w.downTracks[layer][idx] = w.downTracks[layer][len(w.downTracks[layer])-1]
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
