package sfu

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync"

	log "github.com/pion/ion-log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// SimpleSender represents a Sender which writes RTP to a webrtc track
type SimpleSender struct {
	id             string
	sender         *webrtc.RTPSender
	track          *webrtc.Track
	router         *receiverRouter
	enabled        atomicBool
	payload        uint8
	maxBitrate     uint64
	target         uint64
	onCloseHandler func()
	// Muting helpers
	reSync   atomicBool
	snOffset uint16
	tsOffset uint32
	lastSN   uint16
	lastTS   uint32

	start sync.Once
	close sync.Once
}

// NewSimpleSender creates a new track sender instance
func NewSimpleSender(id string, router *receiverRouter, sender *webrtc.RTPSender) Sender {
	s := &SimpleSender{
		id:      id,
		payload: sender.Track().Codec().PayloadType,
		router:  router,
		sender:  sender,
		track:   sender.Track(),
	}

	go s.receiveRTCP()

	return s
}

func (s *SimpleSender) ID() string {
	return s.id
}

func (s *SimpleSender) Start() {
	s.start.Do(func() {
		log.Debugf("starting sender %s with ssrc %d", s.id, s.track.SSRC())
		s.reSync.set(true)
		s.enabled.set(true)
	})
}

// WriteRTP to the track
func (s *SimpleSender) WriteRTP(pkt *rtp.Packet) {
	if !s.enabled.get() {
		return
	}

	if s.reSync.get() {
		if s.track.Kind() == webrtc.RTPCodecTypeVideo {
			// Forward pli to request a keyframe at max 1 pli per second
			recv := s.router.receivers[0]
			if recv == nil {
				return
			}
			relay := false
			// Wait for a keyframe to sync new source
			switch s.payload {
			case webrtc.DefaultPayloadTypeVP8:
				vp8Packet := VP8Helper{}
				if err := vp8Packet.Unmarshal(pkt.Payload); err == nil {
					relay = vp8Packet.IsKeyFrame
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
			}
			if !relay {
				recv.SendRTCP([]rtcp.Packet{
					&rtcp.PictureLossIndication{SenderSSRC: pkt.SSRC, MediaSSRC: pkt.SSRC},
				})
				return
			}
		}
		s.snOffset = pkt.SequenceNumber - s.lastSN - 1
		s.tsOffset = pkt.Timestamp - s.lastTS + 1
		s.reSync.set(false)
	}

	// Backup payload
	bSN := pkt.SequenceNumber
	bTS := pkt.Timestamp
	bPt := pkt.PayloadType
	// Transform payload type
	s.lastSN = pkt.SequenceNumber - s.snOffset
	s.lastTS = pkt.Timestamp - s.tsOffset
	pkt.PayloadType = s.payload
	pkt.Timestamp = s.lastTS
	pkt.SequenceNumber = s.lastSN

	err := s.track.WriteRTP(pkt)
	// Restore packet
	pkt.PayloadType = bPt
	pkt.Timestamp = bTS
	pkt.SequenceNumber = bSN
	if err != nil {
		if err == io.ErrClosedPipe {
			return
		}
		log.Errorf("sender.track.WriteRTP err=%v", err)
	}
}

func (s *SimpleSender) Mute(val bool) {
	if s.enabled.get() != val {
		return
	}
	s.enabled.set(!val)
	if val {
		s.reSync.set(val)
	}
}

func (s *SimpleSender) Kind() webrtc.RTPCodecType {
	return s.track.Kind()
}

func (s *SimpleSender) Track() *webrtc.Track {
	return s.track
}

func (s *SimpleSender) Type() SenderType {
	return SimpleSenderType
}

func (s *SimpleSender) CurrentSpatialLayer() uint8 {
	return 0
}

func (s *SimpleSender) SwitchSpatialLayer(layer uint8) {
	log.Warnf("can't change layers in simple senders, current: %d target: %d", 0, layer)
}

func (s *SimpleSender) SwitchTemporalLayer(layer uint8) {
	log.Warnf("can't change layers in simple senders, target: %d", layer)
}

// Close track
func (s *SimpleSender) Close() {
	s.close.Do(func() {
		log.Debugf("Closing sender %s", s.id)
		if s.onCloseHandler != nil {
			s.onCloseHandler()
		}
	})
}

// OnCloseHandler method to be called on remote tracked removed
func (s *SimpleSender) OnCloseHandler(fn func()) {
	s.onCloseHandler = fn
}

func (s *SimpleSender) receiveRTCP() {
	for {
		pkts, err := s.sender.ReadRTCP()
		if err == io.ErrClosedPipe || err == io.EOF {
			// Remove sender from receiver
			if recv := s.router.receivers[0]; recv != nil {
				recv.DeleteSender(s.id)
			}
			s.Close()
			return
		}

		if err != nil {
			log.Errorf("rtcp err => %v", err)
		}

		recv := s.router.receivers[0]
		if recv == nil {
			continue
		}

		var fwdPkts []rtcp.Packet
		for _, pkt := range pkts {
			switch pkt := pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				if !s.reSync.get() && s.enabled.get() {
					fwdPkts = append(fwdPkts, pkt)
				}
			case *rtcp.TransportLayerNack:
				log.Tracef("sender got nack: %+v", pkt)
				for _, pair := range pkt.Nacks {
					if err := recv.WriteBufferedPacket(
						pair.PacketList(),
						s.track,
						s.snOffset,
						s.tsOffset,
						s.track.SSRC(),
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
