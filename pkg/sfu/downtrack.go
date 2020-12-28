package sfu

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ion-sfu/pkg/buffer"

	"github.com/pion/transport/packetio"

	log "github.com/pion/ion-log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
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
	id                  string
	peerID              string
	bound               atomicBool
	mime                string
	nList               *nackList
	ssrc                uint32
	payload             uint8
	streamID            string
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
		nList:    newNACKList(),
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
		d.payload = uint8(codec.PayloadType)
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
func (d *DownTrack) WriteRTP(p rtp.Packet) error {
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

func (d *DownTrack) writeSimpleRTP(pkt rtp.Packet) error {
	if d.reSync.get() {
		if d.Kind() == webrtc.RTPCodecTypeVideo {
			relay := false
			// Wait for a keyframe to sync new source
			switch d.mime {
			case "video/vp8":
				vp8Packet := VP8Helper{}
				if err := vp8Packet.Unmarshal(pkt.Payload); err == nil {
					relay = vp8Packet.IsKeyFrame
				}
			case "video/h264":
				relay = isH264Keyframe(pkt.Payload)
			}
			if !relay {
				d.receiver.SendRTCP([]rtcp.Packet{
					&rtcp.PictureLossIndication{SenderSSRC: d.ssrc, MediaSSRC: pkt.SSRC},
				})
				return nil
			}
		}
		d.snOffset = pkt.SequenceNumber - d.lastSN - 1
		d.tsOffset = pkt.Timestamp - d.lastTS - 1
		d.lastSSRC = pkt.SSRC
		d.reSync.set(false)
	}

	atomic.AddUint32(&d.octetCount, uint32(len(pkt.Payload)))
	atomic.AddUint32(&d.packetCount, 1)

	d.lastSSRC = pkt.SSRC
	newSN := pkt.SequenceNumber - d.snOffset
	newTS := pkt.Timestamp - d.tsOffset
	if (newSN-d.lastSN)&0x8000 == 0 || d.lastSN == 0 {
		d.lastSN = newSN
		atomic.StoreInt64(&d.lastPacketMs, time.Now().UnixNano()/1e6)
		atomic.StoreUint32(&d.lastTS, newTS)
	}
	pkt.PayloadType = d.payload
	pkt.Timestamp = newTS
	pkt.SequenceNumber = newSN
	pkt.SSRC = d.ssrc

	_, err := d.writeStream.WriteRTP(&pkt.Header, pkt.Payload)
	if err != nil {
		log.Errorf("Write packet err %v", err)
	}
	return err
}

func (d *DownTrack) writeSimulcastRTP(pkt rtp.Packet) error {
	// Check if packet SSRC is different from before
	// if true, the video source changed
	if d.lastSSRC != pkt.SSRC {
		if d.currentSpatialLayer == d.simulcast.targetSpatialLayer && d.lastSSRC != 0 {
			return nil
		}
		relay := false
		// Wait for a keyframe to sync new source
		switch d.mime {
		case "video/vp8":
			vp8Packet := VP8Helper{}
			if err := vp8Packet.Unmarshal(pkt.Payload); err == nil {
				if vp8Packet.IsKeyFrame {
					relay = true
					// Set VP8Helper temporal info
					d.simulcast.temporalSupported = vp8Packet.TemporalSupported
					d.simulcast.refPicID += vp8Packet.PictureID - d.simulcast.lastPicID
					d.simulcast.refTlzi += vp8Packet.TL0PICIDX - d.simulcast.lastTlzi
				}
			}
		case "video/h264":
			relay = isH264Keyframe(pkt.Payload)
		default:
			log.Warnf("codec payload don't support simulcast: %s", d.codec.MimeType)
			return nil
		}
		// Packet is not a keyframe, discard it
		if !relay {
			d.receiver.SendRTCP([]rtcp.Packet{
				&rtcp.PictureLossIndication{SenderSSRC: d.ssrc, MediaSSRC: pkt.SSRC},
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
	// Compute how much time passed between the old RTP pkt
	// and the current packet, and fix timestamp on source change
	if !d.simulcast.lTSCalc.IsZero() && d.lastSSRC != pkt.SSRC {
		tDiff := time.Now().Sub(d.simulcast.lTSCalc)
		td := uint32((tDiff.Milliseconds() * 90) / 1000)
		if td == 0 {
			td = 1
		}
		d.tsOffset = pkt.Timestamp - (d.lastTS + td)
		d.snOffset = pkt.SequenceNumber - d.lastSN - 1
	} else if d.simulcast.lTSCalc.IsZero() {
		d.lastTS = pkt.Timestamp
		d.lastSN = pkt.SequenceNumber
	}
	if d.simulcast.temporalEnabled && d.simulcast.temporalSupported {
		if d.codec.MimeType == "video/vp8" {
			pl, skip := setVP8TemporalLayer(pkt.Payload, d)
			if skip {
				// Pkt not in temporal layer update sequence number offset to avoid gaps
				d.snOffset++
				return nil
			}
			if pl != nil {
				pkt.Payload = pl
			}
		}
	}
	atomic.AddUint32(&d.octetCount, uint32(len(pkt.Payload)))
	atomic.AddUint32(&d.packetCount, 1)
	newSN := pkt.SequenceNumber - d.snOffset
	newTS := pkt.Timestamp - d.tsOffset
	if (newSN-d.lastSN)&0x8000 == 0 {
		d.lastSN = newSN
		atomic.StoreInt64(&d.lastPacketMs, time.Now().UnixNano()/1e6)
		atomic.StoreUint32(&d.lastTS, newTS)
	}
	// Update base
	d.simulcast.lTSCalc = time.Now()
	d.lastSSRC = pkt.SSRC
	// Update pkt headers
	pkt.SequenceNumber = newSN
	pkt.Timestamp = newTS
	pkt.Header.SSRC = d.ssrc
	pkt.Header.PayloadType = d.payload

	_, err := d.writeStream.WriteRTP(&pkt.Header, pkt.Payload)
	if err != nil {
		log.Errorf("Write packet err %v", err)
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
			var nackedPackets []uint16
			for _, pair := range p.Nacks {
				nackedPackets = append(nackedPackets, d.nList.getNACKSeqNo(pair.PacketList())...)
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
