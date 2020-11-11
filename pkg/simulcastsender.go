package sfu

import (
	"bytes"
	"encoding/binary"
	"io"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/pion/ion-log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// SimulcastSender represents a Sender which writes RTP to a webrtc track
type SimulcastSender struct {
	id             string
	router         *receiverRouter
	sender         *webrtc.RTPSender
	transceiver    *webrtc.RTPTransceiver
	track          *webrtc.Track
	enabled        atomicBool
	target         uint64
	payload        uint8
	sdesMidHdrCtr  uint8
	maxBitrate     uint64
	onCloseHandler func()

	currentSpatialLayer uint8
	targetSpatialLayer  uint8
	temporalSupported   bool
	currentTempLayer    uint8
	targetTempLayer     uint8
	temporalEnabled     bool
	simulcastSSRC       uint32
	tsOffset            uint32
	snOffset            uint16
	lTSCalc             time.Time
	lSSRC               uint32
	lTS                 uint32
	lSN                 uint32

	// VP8Helper temporal helpers
	refPicID  uint16
	lastPicID uint16
	refTlzi   uint8
	lastTlzi  uint8

	start sync.Once
	close sync.Once
}

// NewSimulcastSender creates a new track sender instance
func NewSimulcastSender(id string, router *receiverRouter, transceiver *webrtc.RTPTransceiver, layer uint8, conf SimulcastConfig) Sender {
	sender := transceiver.Sender()
	s := &SimulcastSender{
		id:                  id,
		router:              router,
		sender:              sender,
		transceiver:         transceiver,
		track:               sender.Track(),
		payload:             sender.Track().Codec().PayloadType,
		currentTempLayer:    3,
		targetTempLayer:     3,
		currentSpatialLayer: layer,
		targetSpatialLayer:  layer,
		simulcastSSRC:       sender.Track().SSRC(),
		temporalEnabled:     conf.EnableTemporalLayer,
		refPicID:            uint16(rand.Uint32()),
		refTlzi:             uint8(rand.Uint32()),
	}

	go s.receiveRTCP()
	return s
}

func (s *SimulcastSender) ID() string {
	return s.id
}

func (s *SimulcastSender) Start() {
	s.start.Do(func() {
		s.enabled.set(true)
	})
}

// WriteRTP to the track
func (s *SimulcastSender) WriteRTP(pkt *rtp.Packet) {
	// Simulcast write RTP is sync, so the packet can be safely modified and restored
	if !s.enabled.get() {
		return
	}
	// Check if packet SSRC is different from before
	// if true, the video source changed
	if s.lSSRC != pkt.SSRC {
		recv := s.router.receivers[s.targetSpatialLayer]
		if recv == nil || recv.Track().SSRC() != pkt.SSRC {
			return
		}
		relay := false
		// Wait for a keyframe to sync new source
		switch s.payload {
		case webrtc.DefaultPayloadTypeVP8:
			vp8Packet := VP8Helper{}
			if err := vp8Packet.Unmarshal(pkt.Payload); err == nil {
				if vp8Packet.IsKeyFrame {
					relay = true
					// Set VP8Helper temporal info
					s.temporalSupported = vp8Packet.TemporalSupported
					s.refPicID += vp8Packet.PictureID - s.lastPicID
					s.refTlzi += vp8Packet.TL0PICIDX - s.lastTlzi
				}
			}
		case webrtc.DefaultPayloadTypeH264:
			var word uint32
			payload := bytes.NewReader(pkt.Payload)
			err := binary.Read(payload, binary.BigEndian, &word)
			if err != nil || (word&0x1F000000)>>24 != 24 {
				relay = false
			} else {
				relay = word&0x1F == 7
			}
		default:
			log.Warnf("codec payload don't support simulcast: %d", s.track.Codec().PayloadType)
			return
		}
		// Packet is not a keyframe, discard it
		if !relay {
			recv.SendRTCP([]rtcp.Packet{
				&rtcp.PictureLossIndication{SenderSSRC: pkt.SSRC, MediaSSRC: pkt.SSRC},
			})
			return
		}
		// Switch is done remove sender from previous layer
		// and update current layer
		if pRecv := s.router.receivers[s.currentSpatialLayer]; pRecv != nil && s.currentSpatialLayer != s.targetSpatialLayer {
			pRecv.DeleteSender(s.id)
		}
		s.currentSpatialLayer = s.targetSpatialLayer
	}
	// Compute how much time passed between the old RTP pkt
	// and the current packet, and fix timestamp on source change
	if !s.lTSCalc.IsZero() && s.lSSRC != pkt.SSRC {
		tDiff := time.Now().Sub(s.lTSCalc)
		td := uint32((tDiff.Milliseconds() * 90) / 1000)
		if td == 0 {
			td = 1
		}
		s.tsOffset = pkt.Timestamp - (s.lTS + td)
		s.snOffset = pkt.SequenceNumber - uint16(s.lSN) - 1
	} else if s.lTSCalc.IsZero() {
		s.lTS = pkt.Timestamp
		s.lSN = uint32(pkt.SequenceNumber)
	}
	if s.temporalEnabled && s.temporalSupported {
		if s.payload == webrtc.DefaultPayloadTypeVP8 {
			pl, skip := setVP8TemporalLayer(pkt.Payload, s)
			if skip {
				// Pkt not in temporal layer update sequence number offset to avoid gaps
				s.snOffset++
				return
			}
			if pl != nil {
				pkt.Payload = pl
			}
		}
	}
	// Update base
	s.lTSCalc = time.Now()
	s.lSSRC = pkt.SSRC
	s.lTS = pkt.Timestamp - s.tsOffset
	lSN := pkt.SequenceNumber - s.snOffset
	atomic.StoreUint32(&s.lSN, uint32(lSN))
	// Update pkt headers
	h := pkt.Header
	h.SSRC = s.simulcastSSRC
	h.SequenceNumber = lSN
	h.Timestamp = s.lTS
	h.PayloadType = s.payload
	if s.sdesMidHdrCtr < 50 {
		if err := h.SetExtension(1, []byte(s.transceiver.Mid())); err != nil {
			log.Errorf("Setting sdes mid header err: %v", err)
		}
		s.sdesMidHdrCtr++
	}
	// Write packet to client
	if err := s.track.WriteRTP(&rtp.Packet{Header: h, Payload: pkt.Payload}); err != nil {
		if err == io.ErrClosedPipe {
			return
		}
		log.Errorf("sender.track.WriteRTP err=%v", err)
	}
}

func (s *SimulcastSender) SwitchSpatialLayer(targetLayer uint8) {
	// Don't switch until previous switch is done or canceled
	if s.currentSpatialLayer != s.targetSpatialLayer {
		return
	}
	if recv := s.router.receivers[targetLayer]; recv != nil {
		recv.AddSender(s)
		s.targetSpatialLayer = targetLayer
	}
}

func (s *SimulcastSender) Kind() webrtc.RTPCodecType {
	return s.track.Kind()
}

func (s *SimulcastSender) Track() *webrtc.Track {
	return s.track
}

func (s *SimulcastSender) Transceiver() *webrtc.RTPTransceiver {
	return s.transceiver
}

func (s *SimulcastSender) Type() SenderType {
	return SimulcastSenderType
}

func (s *SimulcastSender) Mute(val bool) {
	if s.enabled.get() != val {
		return
	}
	s.enabled.set(!val)
	if !val {
		// reset last mediaSSRC to force a re-sync
		s.lSSRC = 0
	}
}

func (s *SimulcastSender) SwitchTemporalLayer(layer uint8) {
	s.currentTempLayer = layer
}

func (s *SimulcastSender) CurrentSpatialLayer() uint8 {
	return s.currentSpatialLayer
}

// Close track
func (s *SimulcastSender) Close() {
	s.close.Do(func() {
		if s.onCloseHandler != nil {
			s.onCloseHandler()
		}
	})
}

// OnCloseHandler method to be called on remote tracked removed
func (s *SimulcastSender) OnCloseHandler(fn func()) {
	s.onCloseHandler = fn
}

func (s *SimulcastSender) receiveRTCP() {
	for {
		pkts, err := s.sender.ReadRTCP()
		if err == io.ErrClosedPipe || err == io.EOF {
			log.Debugf("Sender %s closed due to: %v", s.id, err)
			// Remove sender from receiver
			if recv := s.router.receivers[s.currentSpatialLayer]; recv != nil {
				recv.DeleteSender(s.id)
			}
			s.Close()
			return
		}

		if err != nil {
			log.Errorf("rtcp err => %v", err)
			continue
		}

		recv := s.router.receivers[s.currentSpatialLayer]
		if recv == nil {
			continue
		}

		var fwdPkts []rtcp.Packet
		for _, pkt := range pkts {
			switch pkt := pkt.(type) {
			case *rtcp.PictureLossIndication:
				if s.enabled.get() {
					pkt.MediaSSRC = s.lSSRC
					pkt.SenderSSRC = s.lSSRC
					fwdPkts = append(fwdPkts, pkt)
				}
			case *rtcp.FullIntraRequest:
				if s.enabled.get() {
					pkt.MediaSSRC = s.lSSRC
					pkt.SenderSSRC = s.lSSRC
					fwdPkts = append(fwdPkts, pkt)
				}
			case *rtcp.ReceiverReport:
				if s.enabled.get() && len(pkt.Reports) > 0 && pkt.Reports[0].FractionLost > 25 {
					log.Tracef("Slow link for sender %s, fraction packet lost %.2f", s.id, float64(pkt.Reports[0].FractionLost)/256)
				}
			case *rtcp.TransportLayerNack:
				log.Tracef("sender got nack: %+v", pkt)
				for _, pair := range pkt.Nacks {
					if err := recv.WriteBufferedPacket(
						pair.PacketList(),
						s.track,
						s.snOffset,
						s.tsOffset,
						s.simulcastSSRC,
					); err == errPacketNotFound {
						// TODO handle missing nacks in sfu cache
					}
				}
			default:
				// TODO: Use fb packets for congestion control
			}
		}
		if len(fwdPkts) > 0 {
			recv.SendRTCP(fwdPkts)
		}
	}
}
