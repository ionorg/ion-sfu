package sfu

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/pion/ion-log"
	"github.com/pion/ion-sfu/pkg/buffer"
	"github.com/pion/rtcp"
	"github.com/pion/transport/packetio"
	"github.com/pion/webrtc/v3"
)

// DownTrackType determines the type of a track
type DownTrackType int

const (
	SimpleDownTrack DownTrackType = iota + 1
	SimulcastDownTrack
	SVCDownTrack
)

// DownTrack  implements TrackLocal, is the track used to write packets
// to SFU Subscriber, the track handle the packets for simple, simulcast
// and SVC Publisher.
type DownTrack struct {
	sync.Mutex

	id                  string
	peerID              string
	bound               atomicBool
	mime                string
	ssrc                uint32
	streamID            string
	payloadType         uint8
	sequencer           *sequencer
	trackType           DownTrackType
	currentSpatialLayer int

	enabled  atomicBool
	reSync   atomicBool
	snOffset uint16
	tsOffset uint32
	lastSSRC uint32
	lastSN   uint16
	lastTS   uint32

	simulcast simulcastTrackHelpers

	codec          webrtc.RTPCodecCapability
	receiver       Receiver
	transceiver    *webrtc.RTPTransceiver
	writeStream    webrtc.TrackLocalWriter
	onCloseHandler func()
	onBind         func()
	closeOnce      sync.Once

	// Report helpers
	octetCount   uint32
	packetCount  uint32
	maxPacketTs  uint32
	lastPacketMs int64
}

// NewDownTrack returns a DownTrack.
func NewDownTrack(c webrtc.RTPCodecCapability, r Receiver, peerID string) (*DownTrack, error) {
	return &DownTrack{
		id:       r.TrackID(),
		peerID:   peerID,
		streamID: r.StreamID(),
		receiver: r,
		codec:    c,
	}, nil
}

// Bind is called by the PeerConnection after negotiation is complete
// This asserts that the code requested is supported by the remote peer.
// If so it setups all the state (SSRC and PayloadType) to have a call
func (d *DownTrack) Bind(t webrtc.TrackLocalContext) (webrtc.RTPCodecParameters, error) {
	parameters := webrtc.RTPCodecParameters{RTPCodecCapability: d.codec}
	if codec, err := codecParametersFuzzySearch(parameters, t.CodecParameters()); err == nil {
		d.ssrc = uint32(t.SSRC())
		d.payloadType = uint8(codec.PayloadType)
		d.writeStream = t.WriteStream()
		d.mime = strings.ToLower(codec.MimeType)
		d.bound.set(true)
		d.reSync.set(true)
		d.enabled.set(true)
		if rr := bufferFactory.GetOrNew(packetio.RTCPBufferPacket, uint32(t.SSRC())).(*buffer.RTCPReader); rr != nil {
			rr.OnPacket(func(pkt []byte) {
				d.handleRTCP(pkt)
			})
		}
		if strings.HasPrefix(d.codec.MimeType, "video/") {
			d.sequencer = newSequencer()
		}
		d.onBind()
		return codec, nil
	}
	return webrtc.RTPCodecParameters{}, webrtc.ErrUnsupportedCodec
}

// Unbind implements the teardown logic when the track is no longer needed. This happens
// because a track has been stopped.
func (d *DownTrack) Unbind(_ webrtc.TrackLocalContext) error {
	d.bound.set(false)
	return nil
}

// ID is the unique identifier for this Track. This should be unique for the
// stream, but doesn't have to globally unique. A common example would be 'audio' or 'video'
// and StreamID would be 'desktop' or 'webcam'
func (d *DownTrack) ID() string { return d.id }

// Codec returns current track codec capability
func (d *DownTrack) Codec() webrtc.RTPCodecCapability { return d.codec }

// StreamID is the group this track belongs too. This must be unique
func (d *DownTrack) StreamID() string { return d.streamID }

// Kind controls if this TrackLocal is audio or video
func (d *DownTrack) Kind() webrtc.RTPCodecType {
	switch {
	case strings.HasPrefix(d.codec.MimeType, "audio/"):
		return webrtc.RTPCodecTypeAudio
	case strings.HasPrefix(d.codec.MimeType, "video/"):
		return webrtc.RTPCodecTypeVideo
	default:
		return webrtc.RTPCodecType(0)
	}
}

// WriteRTP writes a RTP Packet to the DownTrack
func (d *DownTrack) WriteRTP(p buffer.ExtPacket) error {
	if !d.enabled.get() || !d.bound.get() {
		return nil
	}
	switch d.trackType {
	case SimpleDownTrack:
		return d.writeSimpleRTP(p)
	case SimulcastDownTrack:
		return d.writeSimulcastRTP(p)
	}
	return nil
}

func (d *DownTrack) Mute(val bool) {
	if d.enabled.get() != val {
		return
	}
	d.enabled.set(!val)
	if val {
		d.reSync.set(val)
	}
}

// Close track
func (d *DownTrack) Close() {
	d.closeOnce.Do(func() {
		log.Debugf("Closing sender %s", d.peerID)
		if d.onCloseHandler != nil {
			d.onCloseHandler()
		}
	})
}

func (d *DownTrack) SwitchSpatialLayer(targetLayer int) {
	if d.trackType == SimulcastDownTrack {
		// Don't switch until previous switch is done or canceled
		if d.currentSpatialLayer != d.simulcast.targetSpatialLayer {
			return
		}
		if err := d.receiver.SubDownTrack(d, targetLayer); err == nil {
			d.simulcast.targetSpatialLayer = targetLayer
		}
	}
}

// OnCloseHandler method to be called on remote tracked removed
func (d *DownTrack) OnCloseHandler(fn func()) {
	d.onCloseHandler = fn
}

func (d *DownTrack) OnBind(fn func()) {
	d.onBind = fn
}

func (d *DownTrack) writeSimpleRTP(extPkt buffer.ExtPacket) error {
	if d.reSync.get() {
		if d.Kind() == webrtc.RTPCodecTypeVideo {
			if !extPkt.KeyFrame {
				d.receiver.SendRTCP([]rtcp.Packet{
					&rtcp.PictureLossIndication{SenderSSRC: d.ssrc, MediaSSRC: extPkt.Packet.SSRC},
				})
				return nil
			}
		}
		d.snOffset = extPkt.Packet.SequenceNumber - d.lastSN - 1
		d.tsOffset = extPkt.Packet.Timestamp - d.lastTS - 1
		d.lastSSRC = extPkt.Packet.SSRC
		d.reSync.set(false)
	}

	atomic.AddUint32(&d.octetCount, uint32(len(extPkt.Packet.Payload)))
	atomic.AddUint32(&d.packetCount, 1)

	d.lastSSRC = extPkt.Packet.SSRC
	newSN := extPkt.Packet.SequenceNumber - d.snOffset
	newTS := extPkt.Packet.Timestamp - d.tsOffset
	if d.sequencer != nil && !extPkt.Retransmitted {
		d.sequencer.push(extPkt.Packet.SequenceNumber, newSN, extPkt.Head)
	}
	if (newSN-d.lastSN)&0x8000 == 0 || d.lastSN == 0 {
		d.lastSN = newSN
		atomic.StoreInt64(&d.lastPacketMs, time.Now().UnixNano()/1e6)
		atomic.StoreUint32(&d.lastTS, newTS)
	}
	extPkt.Packet.PayloadType = d.payloadType
	extPkt.Packet.Timestamp = newTS
	extPkt.Packet.SequenceNumber = newSN
	extPkt.Packet.SSRC = d.ssrc

	_, err := d.writeStream.WriteRTP(&extPkt.Packet.Header, extPkt.Packet.Payload)
	if err != nil {
		log.Errorf("Write packet err %v", err)
	}
	return err
}

func (d *DownTrack) writeSimulcastRTP(extPkt buffer.ExtPacket) error {
	// Check if packet SSRC is different from before
	// if true, the video source changed
	if d.lastSSRC != extPkt.Packet.SSRC {
		if d.currentSpatialLayer == d.simulcast.targetSpatialLayer && d.lastSSRC != 0 {
			return nil
		}
		// Wait for a keyframe to sync new source
		if !extPkt.KeyFrame {
			// Packet is not a keyframe, discard it
			d.receiver.SendRTCP([]rtcp.Packet{
				&rtcp.PictureLossIndication{SenderSSRC: d.ssrc, MediaSSRC: extPkt.Packet.SSRC},
			})
			return nil
		}
		// Switch is done remove sender from previous layer
		// and update current layer
		if d.currentSpatialLayer != d.simulcast.targetSpatialLayer {
			go d.receiver.DeleteDownTrack(d.currentSpatialLayer, d.peerID)
		}
		d.currentSpatialLayer = d.simulcast.targetSpatialLayer
	}
	// Compute how much time passed between the old RTP extPkt
	// and the current packet, and fix timestamp on source change
	if !d.simulcast.lTSCalc.IsZero() && d.lastSSRC != extPkt.Packet.SSRC {
		tDiff := time.Now().Sub(d.simulcast.lTSCalc)
		td := uint32((tDiff.Milliseconds() * 90) / 1000)
		if td == 0 {
			td = 1
		}
		d.tsOffset = extPkt.Packet.Timestamp - (d.lastTS + td)
		d.snOffset = extPkt.Packet.SequenceNumber - d.lastSN - 1
	} else if d.simulcast.lTSCalc.IsZero() {
		d.lastTS = extPkt.Packet.Timestamp
		d.lastSN = extPkt.Packet.SequenceNumber
	}

	atomic.AddUint32(&d.octetCount, uint32(len(extPkt.Packet.Payload)))
	atomic.AddUint32(&d.packetCount, 1)
	newSN := uint16(0)
	if extPkt.Retransmitted {
		newSN = extPkt.RtxSN
	} else {
		newSN = extPkt.Packet.SequenceNumber - d.snOffset
	}
	newTS := extPkt.Packet.Timestamp - d.tsOffset
	if d.simulcast.temporalSupported {
		if d.mime == "video/vp8" {
			d.Lock()
			pl, skip := setVP8TemporalLayer(extPkt.Packet.Payload, newSN, extPkt.Retransmitted, d)
			d.Unlock()
			if skip {
				// Pkt not in temporal layer update sequence number offset to avoid gaps
				d.snOffset++
				return nil
			}
			extPkt.Payload = pl
		}
	}

	if d.sequencer != nil && !extPkt.Retransmitted {
		d.sequencer.push(extPkt.Packet.SequenceNumber, newSN, extPkt.Head)
	}
	if (newSN-d.lastSN)&0x8000 == 0 {
		d.lastSN = newSN
		atomic.StoreInt64(&d.lastPacketMs, time.Now().UnixNano()/1e6)
		atomic.StoreUint32(&d.lastTS, newTS)
	}
	// Update base
	d.simulcast.lTSCalc = time.Now()
	d.lastSSRC = extPkt.Packet.SSRC
	// Update extPkt headers
	extPkt.Packet.SequenceNumber = newSN
	extPkt.Packet.Timestamp = newTS
	extPkt.Packet.Header.SSRC = d.ssrc
	extPkt.Packet.Header.PayloadType = d.payloadType

	_, err := d.writeStream.WriteRTP(&extPkt.Packet.Header, extPkt.Packet.Payload)
	if err != nil {
		log.Errorf("Write packet err %v", err)
	}

	if d.simulcast.temporalSupported {
		packetFactory.Put(extPkt.Payload)
	}

	return err
}

func (d *DownTrack) handleRTCP(bytes []byte) {
	if !d.enabled.get() {
		return
	}

	pkts, err := rtcp.Unmarshal(bytes)
	if err != nil {
		log.Errorf("Unmarshal rtcp receiver packets err: %v", err)
	}

	var fwdPkts []rtcp.Packet
	pliOnce := true
	firOnce := true
	for _, pkt := range pkts {
		switch p := pkt.(type) {
		case *rtcp.PictureLossIndication:
			if pliOnce {
				p.MediaSSRC = d.lastSSRC
				p.SenderSSRC = d.lastSSRC
				fwdPkts = append(fwdPkts, p)
				pliOnce = false
			}
		case *rtcp.FullIntraRequest:
			if firOnce {
				p.MediaSSRC = d.lastSSRC
				p.SenderSSRC = d.ssrc
				fwdPkts = append(fwdPkts, p)
				firOnce = false
			}
		case *rtcp.ReceiverReport:
			if len(p.Reports) > 0 && p.Reports[0].FractionLost > 25 {
				log.Tracef("Slow link for sender %s, fraction packet lost %.2f", d.peerID, float64(p.Reports[0].FractionLost)/256)
			}
		case *rtcp.TransportLayerNack:
			log.Tracef("sender got nack: %+v", p)
			var nackedPackets []uint32
			for _, pair := range p.Nacks {
				nackedPackets = append(nackedPackets, d.sequencer.getNACKSeqNo(pair.PacketList())...)
			}
			d.receiver.RetransmitPackets(d, nackedPackets)
		}
	}
	if len(fwdPkts) > 0 {
		d.receiver.SendRTCP(fwdPkts)
	}
}

func (d *DownTrack) getSRStats() (octets, packets uint32) {
	octets = atomic.LoadUint32(&d.octetCount)
	packets = atomic.LoadUint32(&d.packetCount)
	return
}
