package sfu

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ion-sfu/pkg/buffer"
	"github.com/pion/rtcp"
	"github.com/pion/transport/packetio"
	"github.com/pion/webrtc/v3"
)

// DownTrackType determines the type of track
type DownTrackType int

const (
	SimpleDownTrack DownTrackType = iota + 1
	SimulcastDownTrack
)

// DownTrack  implements TrackLocal, is the track used to write packets
// to SFU Subscriber, the track handle the packets for simple, simulcast
// and SVC Publisher.
type DownTrack struct {
	id            string
	peerID        string
	bound         atomicBool
	mime          string
	ssrc          uint32
	streamID      string
	maxTrack      int
	payloadType   uint8
	sequencer     *sequencer
	trackType     DownTrackType
	bufferFactory *buffer.Factory
	payload       *[]byte

	currentSpatialLayer int32
	targetSpatialLayer  int32
	temporalLayer       int32

	enabled  atomicBool
	reSync   atomicBool
	snOffset uint16
	tsOffset uint32
	lastSSRC uint32
	lastSN   uint16
	lastTS   uint32

	simulcast        simulcastTrackHelpers
	maxSpatialLayer  int32
	maxTemporalLayer int32

	codec          webrtc.RTPCodecCapability
	receiver       Receiver
	transceiver    *webrtc.RTPTransceiver
	writeStream    webrtc.TrackLocalWriter
	onCloseHandler func()
	onBind         func()
	closeOnce      sync.Once

	// Report helpers
	octetCount  uint32
	packetCount uint32
	maxPacketTs uint32
}

// NewDownTrack returns a DownTrack.
func NewDownTrack(c webrtc.RTPCodecCapability, r Receiver, bf *buffer.Factory, peerID string, mt int) (*DownTrack, error) {
	return &DownTrack{
		id:            r.TrackID(),
		peerID:        peerID,
		maxTrack:      mt,
		streamID:      r.StreamID(),
		bufferFactory: bf,
		receiver:      r,
		codec:         c,
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
		d.reSync.set(true)
		d.enabled.set(true)
		if rr := d.bufferFactory.GetOrNew(packetio.RTCPBufferPacket, uint32(t.SSRC())).(*buffer.RTCPReader); rr != nil {
			rr.OnPacket(func(pkt []byte) {
				d.handleRTCP(pkt)
			})
		}
		if strings.HasPrefix(d.codec.MimeType, "video/") {
			d.sequencer = newSequencer(d.maxTrack)
		}
		if d.onBind != nil {
			d.onBind()
		}
		d.bound.set(true)
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

// RID is the RTP Stream ID for this track. This is Simulcast specific and not used.
func (d *DownTrack) RID() string { return "" }

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

func (d *DownTrack) Stop() error {
	if d.transceiver != nil {
		return d.transceiver.Stop()
	}
	return fmt.Errorf("d.transceiver not exists")
}

func (d *DownTrack) SetTransceiver(transceiver *webrtc.RTPTransceiver) {
	d.transceiver = transceiver
}

// WriteRTP writes a RTP Packet to the DownTrack
func (d *DownTrack) WriteRTP(p *buffer.ExtPacket, layer int) error {
	if !d.enabled.get() || !d.bound.get() {
		return nil
	}
	switch d.trackType {
	case SimpleDownTrack:
		return d.writeSimpleRTP(p)
	case SimulcastDownTrack:
		return d.writeSimulcastRTP(p, layer)
	}
	return nil
}

func (d *DownTrack) Enabled() bool {
	return d.enabled.get()
}

// Mute enables or disables media forwarding
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
		Logger.V(1).Info("Closing sender", "peer_id", d.peerID)
		if d.payload != nil {
			packetFactory.Put(d.payload)
		}
		if d.onCloseHandler != nil {
			d.onCloseHandler()
		}
	})
}

func (d *DownTrack) SetInitialLayers(spatialLayer, temporalLayer int32) {
	atomic.StoreInt32(&d.currentSpatialLayer, spatialLayer)
	atomic.StoreInt32(&d.targetSpatialLayer, spatialLayer)
	atomic.StoreInt32(&d.temporalLayer, temporalLayer<<16|temporalLayer)
}

func (d *DownTrack) CurrentSpatialLayer() int {
	return int(atomic.LoadInt32(&d.currentSpatialLayer))
}

func (d *DownTrack) SwitchSpatialLayer(targetLayer int32, setAsMax bool) error {
	if d.trackType == SimulcastDownTrack {
		// Don't switch until previous switch is done or canceled
		csl := atomic.LoadInt32(&d.currentSpatialLayer)
		if csl != atomic.LoadInt32(&d.targetSpatialLayer) || csl == targetLayer {
			return ErrSpatialLayerBusy
		}
		if err := d.receiver.SwitchDownTrack(d, int(targetLayer)); err == nil {
			atomic.StoreInt32(&d.targetSpatialLayer, targetLayer)
			if setAsMax {
				atomic.StoreInt32(&d.maxSpatialLayer, targetLayer)
			}
		}
		return nil
	}
	return ErrSpatialNotSupported
}

func (d *DownTrack) SwitchSpatialLayerDone(layer int32) {
	atomic.StoreInt32(&d.currentSpatialLayer, layer)
}

func (d *DownTrack) UptrackLayersChange(availableLayers []uint16) (int64, error) {
	if d.trackType == SimulcastDownTrack {
		currentLayer := uint16(d.currentSpatialLayer)
		maxLayer := uint16(atomic.LoadInt32(&d.maxSpatialLayer))

		var maxFound uint16 = 0
		layerFound := false
		var minFound uint16 = 0
		for _, target := range availableLayers {
			if target <= maxLayer {
				if target > maxFound {
					maxFound = target
					layerFound = true
				}
			} else {
				if minFound > target {
					minFound = target
				}
			}
		}
		var targetLayer uint16
		if layerFound {
			targetLayer = maxFound
		} else {
			targetLayer = minFound
		}
		if currentLayer != targetLayer {
			if err := d.SwitchSpatialLayer(int32(targetLayer), false); err != nil {
				return int64(targetLayer), err
			}
		}
		return int64(targetLayer), nil
	}
	return -1, fmt.Errorf("downtrack %s does not support simulcast", d.id)
}

func (d *DownTrack) SwitchTemporalLayer(targetLayer int32, setAsMax bool) {
	if d.trackType == SimulcastDownTrack {
		layer := atomic.LoadInt32(&d.temporalLayer)
		currentLayer := uint16(layer)
		currentTargetLayer := uint16(layer >> 16)

		// Don't switch until previous switch is done or canceled
		if currentLayer != currentTargetLayer {
			return
		}
		atomic.StoreInt32(&d.temporalLayer, targetLayer<<16|int32(currentLayer))
		if setAsMax {
			atomic.StoreInt32(&d.maxTemporalLayer, targetLayer)
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

func (d *DownTrack) CreateSourceDescriptionChunks() []rtcp.SourceDescriptionChunk {
	if !d.bound.get() {
		return nil
	}
	return []rtcp.SourceDescriptionChunk{
		{
			Source: d.ssrc,
			Items: []rtcp.SourceDescriptionItem{{
				Type: rtcp.SDESCNAME,
				Text: d.streamID,
			}},
		}, {
			Source: d.ssrc,
			Items: []rtcp.SourceDescriptionItem{{
				Type: rtcp.SDESType(15),
				Text: d.transceiver.Mid(),
			}},
		},
	}
}

func (d *DownTrack) CreateSenderReport() *rtcp.SenderReport {
	if !d.bound.get() {
		return nil
	}
	srRTP, srNTP := d.receiver.GetSenderReportTime(int(atomic.LoadInt32(&d.currentSpatialLayer)))
	if srRTP == 0 {
		return nil
	}

	now := time.Now()
	nowNTP := toNtpTime(now)

	diff := (uint64(now.Sub(ntpTime(srNTP).Time())) * uint64(d.codec.ClockRate)) / uint64(time.Second)
	if diff < 0 {
		diff = 0
	}
	octets, packets := d.getSRStats()

	return &rtcp.SenderReport{
		SSRC:        d.ssrc,
		NTPTime:     uint64(nowNTP),
		RTPTime:     srRTP + uint32(diff),
		PacketCount: packets,
		OctetCount:  octets,
	}
}

func (d *DownTrack) UpdateStats(packetLen uint32) {
	atomic.AddUint32(&d.octetCount, packetLen)
	atomic.AddUint32(&d.packetCount, 1)
}

func (d *DownTrack) writeSimpleRTP(extPkt *buffer.ExtPacket) error {
	if d.reSync.get() {
		if d.Kind() == webrtc.RTPCodecTypeVideo {
			if !extPkt.KeyFrame {
				d.receiver.SendRTCP([]rtcp.Packet{
					&rtcp.PictureLossIndication{SenderSSRC: d.ssrc, MediaSSRC: extPkt.Packet.SSRC},
				})
				return nil
			}
		}

		if d.lastSN != 0 {
			d.snOffset = extPkt.Packet.SequenceNumber - d.lastSN - 1
			d.tsOffset = extPkt.Packet.Timestamp - d.lastTS - 1
		}
		atomic.StoreUint32(&d.lastSSRC, extPkt.Packet.SSRC)
		d.reSync.set(false)
	}

	d.UpdateStats(uint32(len(extPkt.Packet.Payload)))

	newSN := extPkt.Packet.SequenceNumber - d.snOffset
	newTS := extPkt.Packet.Timestamp - d.tsOffset
	if d.sequencer != nil {
		d.sequencer.push(extPkt.Packet.SequenceNumber, newSN, newTS, 0, extPkt.Head)
	}
	if extPkt.Head {
		d.lastSN = newSN
		d.lastTS = newTS
	}
	hdr := extPkt.Packet.Header
	hdr.PayloadType = d.payloadType
	hdr.Timestamp = newTS
	hdr.SequenceNumber = newSN
	hdr.SSRC = d.ssrc

	_, err := d.writeStream.WriteRTP(&hdr, extPkt.Packet.Payload)
	return err
}

func (d *DownTrack) writeSimulcastRTP(extPkt *buffer.ExtPacket, layer int) error {
	// Check if packet SSRC is different from before
	// if true, the video source changed
	reSync := d.reSync.get()
	csl := d.CurrentSpatialLayer()

	if csl != layer {
		return nil
	}

	lastSSRC := atomic.LoadUint32(&d.lastSSRC)
	if lastSSRC != extPkt.Packet.SSRC || reSync {
		// Wait for a keyframe to sync new source
		if reSync && !extPkt.KeyFrame {
			// Packet is not a keyframe, discard it
			d.receiver.SendRTCP([]rtcp.Packet{
				&rtcp.PictureLossIndication{SenderSSRC: d.ssrc, MediaSSRC: extPkt.Packet.SSRC},
			})
			return nil
		}
		if reSync && d.simulcast.lTSCalc != 0 {
			d.simulcast.lTSCalc = extPkt.Arrival
		}

		if d.simulcast.temporalSupported {
			if d.mime == "video/vp8" {
				if vp8, ok := extPkt.Payload.(buffer.VP8); ok {
					d.simulcast.pRefPicID = d.simulcast.lPicID
					d.simulcast.refPicID = vp8.PictureID
					d.simulcast.pRefTlZIdx = d.simulcast.lTlZIdx
					d.simulcast.refTlZIdx = vp8.TL0PICIDX
				}
			}
		}
		d.reSync.set(false)
	}
	// Compute how much time passed between the old RTP extPkt
	// and the current packet, and fix timestamp on source change
	if d.simulcast.lTSCalc != 0 && lastSSRC != extPkt.Packet.SSRC {
		atomic.StoreUint32(&d.lastSSRC, extPkt.Packet.SSRC)
		tDiff := (extPkt.Arrival - d.simulcast.lTSCalc) / 1e6
		td := uint32((tDiff * 90) / 1000)
		if td == 0 {
			td = 1
		}
		d.tsOffset = extPkt.Packet.Timestamp - (d.lastTS + td)
		d.snOffset = extPkt.Packet.SequenceNumber - d.lastSN - 1
	} else if d.simulcast.lTSCalc == 0 {
		d.lastTS = extPkt.Packet.Timestamp
		d.lastSN = extPkt.Packet.SequenceNumber
		if d.mime == "video/vp8" {
			if vp8, ok := extPkt.Payload.(buffer.VP8); ok {
				d.simulcast.temporalSupported = vp8.TemporalSupported
			}
		}
	}
	newSN := extPkt.Packet.SequenceNumber - d.snOffset
	newTS := extPkt.Packet.Timestamp - d.tsOffset
	payload := extPkt.Packet.Payload

	var (
		picID   uint16
		tlz0Idx uint8
	)
	if d.simulcast.temporalSupported {
		if d.mime == "video/vp8" {
			drop := false
			if payload, picID, tlz0Idx, drop = setVP8TemporalLayer(extPkt, d); drop {
				// Pkt not in temporal getLayer update sequence number offset to avoid gaps
				d.snOffset++
				return nil
			}
		}
	}

	if d.sequencer != nil {
		if meta := d.sequencer.push(extPkt.Packet.SequenceNumber, newSN, newTS, uint8(csl), extPkt.Head); meta != nil &&
			d.simulcast.temporalSupported && d.mime == "video/vp8" {
			meta.setVP8PayloadMeta(tlz0Idx, picID)
		}
	}

	atomic.AddUint32(&d.octetCount, uint32(len(extPkt.Packet.Payload)))
	atomic.AddUint32(&d.packetCount, 1)

	if extPkt.Head {
		d.lastSN = newSN
		d.lastTS = newTS
	}
	// Update base
	d.simulcast.lTSCalc = extPkt.Arrival
	// Update extPkt headers
	hdr := extPkt.Packet.Header
	hdr.SequenceNumber = newSN
	hdr.Timestamp = newTS
	hdr.SSRC = d.ssrc
	hdr.PayloadType = d.payloadType

	_, err := d.writeStream.WriteRTP(&hdr, payload)
	return err
}

func (d *DownTrack) handleRTCP(bytes []byte) {
	if !d.enabled.get() {
		return
	}

	pkts, err := rtcp.Unmarshal(bytes)
	if err != nil {
		Logger.Error(err, "Unmarshal rtcp receiver packets err")
	}

	var fwdPkts []rtcp.Packet
	pliOnce := true
	firOnce := true

	var (
		maxRatePacketLoss  uint8
		expectedMinBitrate uint64
	)

	ssrc := atomic.LoadUint32(&d.lastSSRC)
	if ssrc == 0 {
		return
	}
	for _, pkt := range pkts {
		switch p := pkt.(type) {
		case *rtcp.PictureLossIndication:
			if pliOnce {
				p.MediaSSRC = ssrc
				p.SenderSSRC = d.ssrc
				fwdPkts = append(fwdPkts, p)
				pliOnce = false
			}
		case *rtcp.FullIntraRequest:
			if firOnce {
				p.MediaSSRC = ssrc
				p.SenderSSRC = d.ssrc
				fwdPkts = append(fwdPkts, p)
				firOnce = false
			}
		case *rtcp.ReceiverEstimatedMaximumBitrate:
			if expectedMinBitrate == 0 || expectedMinBitrate > uint64(p.Bitrate) {
				expectedMinBitrate = uint64(p.Bitrate)
			}
		case *rtcp.ReceiverReport:
			for _, r := range p.Reports {
				if maxRatePacketLoss == 0 || maxRatePacketLoss < r.FractionLost {
					maxRatePacketLoss = r.FractionLost
				}
			}
		case *rtcp.TransportLayerNack:
			if d.sequencer != nil {
				var nackedPackets []packetMeta
				for _, pair := range p.Nacks {
					nackedPackets = append(nackedPackets, d.sequencer.getSeqNoPairs(pair.PacketList())...)
				}
				if err = d.receiver.RetransmitPackets(d, nackedPackets); err != nil {
					return
				}
			}
		}
	}
	if d.trackType == SimulcastDownTrack && (maxRatePacketLoss != 0 || expectedMinBitrate != 0) {
		d.handleLayerChange(maxRatePacketLoss, expectedMinBitrate)
	}

	if len(fwdPkts) > 0 {
		d.receiver.SendRTCP(fwdPkts)
	}
}

func (d *DownTrack) handleLayerChange(maxRatePacketLoss uint8, expectedMinBitrate uint64) {
	currentSpatialLayer := atomic.LoadInt32(&d.currentSpatialLayer)
	targetSpatialLayer := atomic.LoadInt32(&d.targetSpatialLayer)

	temporalLayer := atomic.LoadInt32(&d.temporalLayer)
	currentTemporalLayer := temporalLayer & 0x0f
	targetTemporalLayer := temporalLayer >> 16

	if targetSpatialLayer == currentSpatialLayer && currentTemporalLayer == targetTemporalLayer {
		if time.Now().After(d.simulcast.switchDelay) {
			brs := d.receiver.GetBitrate()
			cbr := brs[currentSpatialLayer]
			mtl := d.receiver.GetMaxTemporalLayer()
			mctl := mtl[currentSpatialLayer]

			if maxRatePacketLoss <= 5 {
				if currentTemporalLayer < mctl && currentTemporalLayer+1 <= atomic.LoadInt32(&d.maxTemporalLayer) &&
					expectedMinBitrate >= 3*cbr/4 {
					d.SwitchTemporalLayer(currentTemporalLayer+1, false)
					d.simulcast.switchDelay = time.Now().Add(3 * time.Second)
				}
				if currentTemporalLayer >= mctl && expectedMinBitrate >= 3*cbr/2 && currentSpatialLayer+1 <= atomic.LoadInt32(&d.maxSpatialLayer) &&
					currentSpatialLayer+1 <= 2 {
					if err := d.SwitchSpatialLayer(currentSpatialLayer+1, false); err == nil {
						d.SwitchTemporalLayer(0, false)
					}
					d.simulcast.switchDelay = time.Now().Add(5 * time.Second)
				}
			}
			if maxRatePacketLoss >= 25 {
				if (expectedMinBitrate <= 5*cbr/8 || currentTemporalLayer == 0) &&
					currentSpatialLayer > 0 &&
					brs[currentSpatialLayer-1] != 0 {
					if err := d.SwitchSpatialLayer(currentSpatialLayer-1, false); err != nil {
						d.SwitchTemporalLayer(mtl[currentSpatialLayer-1], false)
					}
					d.simulcast.switchDelay = time.Now().Add(10 * time.Second)
				} else {
					d.SwitchTemporalLayer(currentTemporalLayer-1, false)
					d.simulcast.switchDelay = time.Now().Add(5 * time.Second)
				}
			}
		}
	}

}

func (d *DownTrack) getSRStats() (octets, packets uint32) {
	octets = atomic.LoadUint32(&d.octetCount)
	packets = atomic.LoadUint32(&d.packetCount)
	return
}
