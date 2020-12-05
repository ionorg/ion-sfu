package sfu

import (
	"bytes"
	"encoding/binary"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	writeStream    atomic.Value
	onCloseHandler func()
	closeOnce      sync.Once
}

// NewDownTrack returns a DownTrack.
func NewDownTrack(c webrtc.RTPCodecCapability, r Receiver, peerID, id, streamID string) (*DownTrack, error) {
	return &DownTrack{
		id:       id,
		peerID:   peerID,
		nList:    newNACKList(),
		codec:    c,
		receiver: r,
		streamID: streamID,
	}, nil
}

// Bind is called by the PeerConnection after negotiation is complete
// This asserts that the code requested is supported by the remote peer.
// If so it setups all the state (SSRC and PayloadType) to have a call
func (d *DownTrack) Bind(t webrtc.TrackLocalContext) (webrtc.RTPCodecParameters, error) {
	parameters := webrtc.RTPCodecParameters{RTPCodecCapability: d.codec}
	if codec, err := codecParametersFuzzySearch(parameters, t.CodecParameters()); err == nil {
		d.bound.set(true)
		d.ssrc = uint32(t.SSRC())
		d.payload = uint8(codec.PayloadType)
		d.writeStream.Store(t.WriteStream())
		d.mime = strings.ToLower(codec.MimeType)
		d.reSync.set(true)
		d.enabled.set(true)
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
func (d *DownTrack) WriteRTP(p *rtp.Packet) error {
	if !d.enabled.get() {
		return nil
	}
	switch d.trackType {
	case SimpleDownTrack:
		return d.writeSimpleRTP(*p)
	case SimulcastDownTrack:
		return d.writeSimulcastRTP(*p)
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
		if err := d.receiver.SubDownTrack(d, targetLayer); err != nil {
			d.simulcast.targetSpatialLayer = targetLayer
		}
	}
}

// OnCloseHandler method to be called on remote tracked removed
func (d *DownTrack) OnCloseHandler(fn func()) {
	d.onCloseHandler = fn
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
				var word uint32
				payload := bytes.NewReader(pkt.Payload)
				err := binary.Read(payload, binary.BigEndian, &word)
				if err != nil || (word&0x1F000000)>>24 != 24 {
					relay = false
				} else {
					relay = word&0x1F == 7
				}
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

	d.lastSN = pkt.SequenceNumber - d.snOffset
	d.lastTS = pkt.Timestamp - d.tsOffset
	pkt.PayloadType = d.payload
	pkt.Extensions = nil
	pkt.Timestamp = d.lastTS
	pkt.SequenceNumber = d.lastSN
	pkt.SSRC = d.ssrc

	if tw, ok := d.writeStream.Load().(webrtc.TrackLocalWriter); ok && d.bound.get() {
		_, err := tw.WriteRTP(&pkt.Header, pkt.Payload)
		if err != nil {
			log.Errorf("Write packet err %v", err)
		}
		return err
	}
	return nil
}

func (d *DownTrack) writeSimulcastRTP(pkt rtp.Packet) error {
	// Check if packet SSRC is different from before
	// if true, the video source changed
	if d.lastSSRC != pkt.SSRC {
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
			var word uint32
			payload := bytes.NewReader(pkt.Payload)
			err := binary.Read(payload, binary.BigEndian, &word)
			if err != nil || (word&0x1F000000)>>24 != 24 {
				relay = false
			} else {
				relay = word&0x1F == 7
			}
		default:
			log.Warnf("codec payload don't support simulcast: %s", d.codec.MimeType)
			return nil
		}
		// Packet is not a keyframe, discard it
		if !relay {
			d.receiver.SendRTCP([]rtcp.Packet{
				&rtcp.PictureLossIndication{SenderSSRC: pkt.SSRC, MediaSSRC: pkt.SSRC},
			})
			return nil
		}
		// Switch is done remove sender from previous layer
		// and update current layer
		if d.currentSpatialLayer != d.simulcast.targetSpatialLayer {
			d.receiver.DeleteDownTrack(d.currentSpatialLayer, d.peerID)
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
	// Update base
	d.simulcast.lTSCalc = time.Now()
	d.lastSSRC = pkt.SSRC
	d.lastTS = pkt.Timestamp - d.tsOffset
	d.lastSN = pkt.SequenceNumber - d.snOffset
	// Update pkt headers
	pkt.SequenceNumber = d.lastSN
	pkt.Extensions = nil
	pkt.Timestamp = d.lastTS
	pkt.Header.SSRC = d.ssrc
	pkt.Header.PayloadType = d.payload

	if tw, ok := d.writeStream.Load().(webrtc.TrackLocalWriter); ok && d.bound.get() {
		_, err := tw.WriteRTP(&pkt.Header, pkt.Payload)
		if err != nil {
			log.Errorf("Write packet err %v", err)
		}
		return err
	}
	return nil
}
