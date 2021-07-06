package sfu

import (
	"io"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gammazero/workerpool"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/rs/zerolog/log"

	"github.com/pion/ion-sfu/pkg/buffer"
	"github.com/pion/ion-sfu/pkg/stats"
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
	HasSpatialLayer(layer int32) bool
	GetBitrate() [3]uint64
	GetMaxTemporalLayer() [3]int32
	RetransmitPackets(track *DownTrack, packets []packetMeta) error
	DeleteDownTrack(peerID string)
	OnCloseHandler(fn func())
	SendRTCP(p []rtcp.Packet)
	SetRTCPCh(ch chan []rtcp.Packet)
}

// WebRTCReceiver receives a video track
type WebRTCReceiver struct {
	peerID         string
	trackID        string
	streamID       string
	kind           webrtc.RTPCodecType
	bandwidth      uint64
	stream         string
	receiver       *webrtc.RTPReceiver
	codec          webrtc.RTPCodecParameters
	stats          [3]*stats.Stream
	nackWorker     *workerpool.WorkerPool
	isSimulcast    bool
	onCloseHandler func()
	closeOnce      sync.Once
	closed         atomicBool

	rtcpMu      sync.Mutex
	rtcpCh      chan []rtcp.Packet
	lastPli     int64
	pliThrottle int64

	bufferMu sync.RWMutex
	buffers  [3]*buffer.Buffer

	upTrackMu sync.RWMutex
	upTracks  [3]*webrtc.TrackRemote

	downTrackMu sync.RWMutex
	downTracks  []*DownTrack
	index       map[string]int
	free        map[int]struct{}
	numProcs    int
}

type ReceiverOpts func(w *WebRTCReceiver) *WebRTCReceiver

func WithPliThrottle(period int64) ReceiverOpts {
	return func(w *WebRTCReceiver) *WebRTCReceiver {
		w.pliThrottle = period * 1e6
		return w
	}
}

// NewWebRTCReceiver creates a new webrtc track receivers
func NewWebRTCReceiver(receiver *webrtc.RTPReceiver, track *webrtc.TrackRemote, pid string, opts ...ReceiverOpts) Receiver {
	w := &WebRTCReceiver{
		peerID:      pid,
		receiver:    receiver,
		trackID:     track.ID(),
		streamID:    track.StreamID(),
		codec:       track.Codec(),
		kind:        track.Kind(),
		nackWorker:  workerpool.New(1),
		isSimulcast: len(track.RID()) > 0,
		pliThrottle: 500e6,
		downTracks:  make([]*DownTrack, 0),
		index:       make(map[string]int),
		free:        make(map[int]struct{}),
		numProcs:    runtime.NumCPU(),
	}
	if runtime.GOMAXPROCS(0) < w.numProcs {
		w.numProcs = runtime.GOMAXPROCS(0)
	}
	for _, opt := range opts {
		w = opt(w)
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
	w.upTrackMu.RLock()
	defer w.upTrackMu.RUnlock()

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
	if w.closed.get() {
		return
	}

	var layer int32
	switch track.RID() {
	case fullResolution:
		layer = 2
	case halfResolution:
		layer = 1
	default:
		layer = 0
	}

	w.upTrackMu.Lock()
	w.upTracks[layer] = track
	w.upTrackMu.Unlock()

	w.bufferMu.Lock()
	w.buffers[layer] = buff
	w.bufferMu.Unlock()

	if w.isSimulcast {
		w.downTrackMu.RLock()
		for _, dt := range w.downTracks {
			if dt != nil {
				if (bestQualityFirst && layer > dt.CurrentSpatialLayer()) ||
					(!bestQualityFirst && layer < dt.CurrentSpatialLayer()) {
					_ = dt.SwitchSpatialLayer(layer, false)
				}
			}
		}
		w.downTrackMu.RUnlock()
	}
	go w.forwardRTP(layer)
}

func (w *WebRTCReceiver) AddDownTrack(track *DownTrack, bestQualityFirst bool) {
	if w.closed.get() {
		return
	}

	layer := 0

	w.downTrackMu.RLock()
	_, ok := w.index[track.peerID]
	w.downTrackMu.RUnlock()
	if ok {
		return
	}

	if w.isSimulcast {
		w.upTrackMu.RLock()
		for i, t := range w.upTracks {
			if t != nil {
				layer = i
				if !bestQualityFirst {
					break
				}
			}
		}
		w.upTrackMu.RUnlock()

		track.SetInitialLayers(int32(layer), 2)
		track.maxSpatialLayer = 2
		track.maxTemporalLayer = 2
		track.lastSSRC = w.SSRC(layer)
		track.trackType = SimulcastDownTrack
		track.payload = packetFactory.Get().([]byte)
	} else {
		track.SetInitialLayers(0, 0)
		track.trackType = SimpleDownTrack
	}

	w.downTrackMu.Lock()
	defer w.downTrackMu.Unlock()

	for idx := range w.free {
		w.index[track.peerID] = idx
		w.downTracks[idx] = track
		delete(w.free, idx)
		return
	}

	w.index[track.peerID] = len(w.downTracks)
	w.downTracks = append(w.downTracks, track)
}

func (w *WebRTCReceiver) HasSpatialLayer(layer int32) bool {
	// TODO: actual implementation, depends on if we are receiving data or not
	// if the client stopped sending a layer due to bandwidth constraints, then we won't be able to switch
	return true
}

func (w *WebRTCReceiver) GetBitrate() [3]uint64 {
	var br [3]uint64
	w.bufferMu.RLock()
	defer w.bufferMu.RUnlock()
	for i, buff := range w.buffers {
		if buff != nil {
			br[i] = buff.Bitrate()
		}
	}
	return br
}

func (w *WebRTCReceiver) GetMaxTemporalLayer() [3]int32 {
	var tls [3]int32
	w.bufferMu.RLock()
	defer w.bufferMu.RUnlock()
	for i, buff := range w.buffers {
		if buff != nil {
			tls[i] = int32(buff.MaxTemporalLayer())
		}
	}
	return tls
}

// OnCloseHandler method to be called on remote tracked removed
func (w *WebRTCReceiver) OnCloseHandler(fn func()) {
	w.onCloseHandler = fn
}

// DeleteDownTrack removes a DownTrack from a Receiver
func (w *WebRTCReceiver) DeleteDownTrack(peerID string) {
	if w.closed.get() {
		return
	}

	w.downTrackMu.Lock()
	defer w.downTrackMu.Unlock()
	idx, ok := w.index[peerID]
	if !ok {
		return
	}
	w.downTracks[idx] = nil
	w.free[idx] = struct{}{}
}

func (w *WebRTCReceiver) SendRTCP(p []rtcp.Packet) {
	if _, ok := p[0].(*rtcp.PictureLossIndication); ok {
		w.rtcpMu.Lock()
		defer w.rtcpMu.Unlock()
		if time.Now().UnixNano()-w.lastPli < w.pliThrottle {
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
			w.bufferMu.RLock()
			buff := w.buffers[meta.layer]
			w.bufferMu.RUnlock()
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

func (w *WebRTCReceiver) forwardRTP(layer int32) {
	defer func() {
		w.closeOnce.Do(func() {
			w.closed.set(true)
			w.closeTracks()
		})
	}()

	pli := []rtcp.Packet{
		&rtcp.PictureLossIndication{SenderSSRC: rand.Uint32(), MediaSSRC: w.SSRC(int(layer))},
	}

	for {
		w.bufferMu.RLock()
		pkt, err := w.buffers[layer].ReadExtended()
		w.bufferMu.RUnlock()
		if err == io.EOF {
			return
		}

		w.downTrackMu.RLock()
		if len(w.downTracks)-len(w.free) < 5 {
			// serial - not enough down tracks for parallelization to outweigh overhead
			for _, dt := range w.downTracks {
				if dt != nil {
					w.writeRTP(layer, dt, pkt, pli)
				}
			}
		} else {
			// parallel - enables much more efficient multi-core utilization
			start := uint64(0)
			end := uint64(len(w.downTracks))

			// 100µs is enough to amortize the overhead and provide sufficient load balancing.
			// WriteRTP takes about 50µs on average, so we write to 2 down tracks per loop.
			step := uint64(2)

			var wg sync.WaitGroup
			wg.Add(w.numProcs)
			for p := 0; p < w.numProcs; p++ {
				go func() {
					defer wg.Done()
					for {
						n := atomic.AddUint64(&start, step)
						if n >= end+step {
							return
						}

						for i := n - step; i < n && i < end; i++ {
							if dt := w.downTracks[i]; dt != nil {
								w.writeRTP(layer, dt, pkt, pli)
							}
						}
					}
				}()
			}
			wg.Wait()
		}
		w.downTrackMu.RUnlock()
	}
}

func (w *WebRTCReceiver) writeRTP(layer int32, dt *DownTrack, pkt *buffer.ExtPacket, pli []rtcp.Packet) {
	if w.isSimulcast {
		targetLayer := dt.TargetSpatialLayer()
		currentLayer := dt.CurrentSpatialLayer()
		if targetLayer == layer && currentLayer != targetLayer {
			if pkt.KeyFrame {
				dt.SwitchSpatialLayerDone(targetLayer)
				currentLayer = targetLayer
			} else {
				w.SendRTCP(pli)
			}
		}
		if currentLayer != layer {
			return
		}
	}

	if err := dt.WriteRTP(pkt); err != nil {
		log.Error().Err(err).Str("id", dt.id).Msg("Error writing to down track")
	}
}

// closeTracks close all tracks from Receiver
func (w *WebRTCReceiver) closeTracks() {
	w.downTrackMu.Lock()
	for _, dt := range w.downTracks {
		if dt != nil {
			dt.Close()
		}
	}
	w.downTracks = make([]*DownTrack, 0)
	w.index = make(map[string]int)
	w.free = make(map[int]struct{})
	w.downTrackMu.Unlock()

	w.nackWorker.StopWait()
	if w.onCloseHandler != nil {
		w.onCloseHandler()
	}
}
