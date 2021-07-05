package sfu

import (
	"hash/fnv"
	"io"
	"math/rand"
	"runtime"
	"sync"
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
	sync.RWMutex
	rtcpMu    sync.Mutex
	closeOnce sync.Once

	peerID    string
	trackID   string
	streamID  string
	kind      webrtc.RTPCodecType
	closed    atomicBool
	bandwidth uint64
	lastPli   int64
	stream    string
	receiver  *webrtc.RTPReceiver
	codec     webrtc.RTPCodecParameters
	rtcpCh    chan []rtcp.Packet
	buffers   [3]*buffer.Buffer
	upTracks  [3]*webrtc.TrackRemote
	stats     [3]*stats.Stream
	// map of peerID => *DownTrack, split into runtime.NumCPU buckets
	downTracks     []map[string]*DownTrack
	buckets        uint32
	nackWorker     *workerpool.WorkerPool
	rtpWritePool   *workerpool.WorkerPool
	isSimulcast    bool
	onCloseHandler func()
	pliThrottle    int64
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
	buckets := runtime.NumCPU()
	w := &WebRTCReceiver{
		peerID:       pid,
		receiver:     receiver,
		trackID:      track.ID(),
		streamID:     track.StreamID(),
		codec:        track.Codec(),
		kind:         track.Kind(),
		nackWorker:   workerpool.New(1),
		rtpWritePool: workerpool.New(buckets),
		isSimulcast:  len(track.RID()) > 0,
		pliThrottle:  500e6,
		downTracks:   make([]map[string]*DownTrack, buckets),
		buckets:      uint32(buckets),
	}
	for b := 0; b < buckets; b++ {
		w.downTracks[b] = make(map[string]*DownTrack)
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

	w.Lock()
	w.upTracks[layer] = track
	w.buffers[layer] = buff
	w.Unlock()

	if w.isSimulcast {
		w.RLock()
		for _, bucket := range w.downTracks {
			for _, dt := range bucket {
				if (bestQualityFirst && layer > dt.CurrentSpatialLayer()) ||
					(!bestQualityFirst && layer < dt.CurrentSpatialLayer()) {
					_ = dt.SwitchSpatialLayer(layer, false)
				}
			}
		}
		w.RUnlock()
	}
	go w.writeRTP(layer)
}

func (w *WebRTCReceiver) AddDownTrack(track *DownTrack, bestQualityFirst bool) {
	if w.closed.get() {
		return
	}

	layer := 0
	w.Lock()
	defer w.Unlock()

	bucket := w.getBucket(track.peerID)
	if w.downTracks[bucket][track.peerID] != nil {
		return
	}

	if w.isSimulcast {
		for i, t := range w.upTracks {
			if t != nil {
				layer = i
				if !bestQualityFirst {
					break
				}
			}
		}
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

	w.downTracks[bucket][track.peerID] = track
}

func (w *WebRTCReceiver) HasSpatialLayer(layer int32) bool {
	// TODO: actual implementation, depends on if we are receiving data or not
	// if the client stopped sending a layer due to bandwidth constraints, then we won't be able to switch
	return true
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

func (w *WebRTCReceiver) GetMaxTemporalLayer() [3]int32 {
	var tls [3]int32
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

	bucket := w.getBucket(peerID)
	w.Lock()
	delete(w.downTracks[bucket], peerID)
	w.Unlock()
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

func (w *WebRTCReceiver) writeRTP(layer int32) {
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
		pkt, err := w.buffers[layer].ReadExtended()
		if err == io.EOF {
			return
		}

		var wg sync.WaitGroup
		w.RLock()
		for _, bucket := range w.downTracks {
			if len(bucket) == 0 {
				continue
			}
			downTracks := bucket
			wg.Add(1)
			w.rtpWritePool.Submit(func() {
				defer wg.Done()
				for _, dt := range downTracks {
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
							continue
						}
					}

					if err = dt.WriteRTP(pkt); err != nil {
						log.Error().Err(err).Str("id", dt.id).Msg("Error writing to down track")
					}
				}
			})
		}
		wg.Wait()
		w.RUnlock()
	}
}

// closeTracks close all tracks from Receiver
func (w *WebRTCReceiver) closeTracks() {
	w.Lock()
	for i, bucket := range w.downTracks {
		for _, dt := range bucket {
			dt.Close()
		}
		w.downTracks[i] = make(map[string]*DownTrack)
	}
	w.Unlock()

	w.nackWorker.StopWait()
	if w.onCloseHandler != nil {
		w.onCloseHandler()
	}
}

func (w *WebRTCReceiver) getBucket(peerID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(peerID))
	return int(h.Sum32() % w.buckets)
}
