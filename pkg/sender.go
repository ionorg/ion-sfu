package sfu

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// Sender defines a interface for a track receivers
type Sender interface {
	ID() string
	Close()
	Kind() webrtc.RTPCodecType
	Mute(val bool)
	WriteRTP(*rtp.Packet)
	CurrentSpatialLayer() uint8
	OnCloseHandler(fn func())
	stats() string
	// Simulcast/SVC events
	SwitchSpatialLayer(layer uint8)
	SwitchTemporalLayer(layer uint8)
}

// WebRTCSender represents a Sender which writes RTP to a webrtc track
type WebRTCSender struct {
	id             string
	ctx            context.Context
	cancel         context.CancelFunc
	sender         *webrtc.RTPSender
	track          *webrtc.Track
	router         Router
	muted          atomicBool
	payload        uint8
	maxBitrate     uint64
	target         uint64
	onCloseHandler func()
	// Muting helpers
	lastPli  time.Time
	reSync   atomicBool
	snOffset uint16
	tsOffset uint32
	lastSN   uint16
	lastTS   uint32

	once sync.Once
}

// NewWebRTCSender creates a new track sender instance
func NewWebRTCSender(ctx context.Context, id string, router Router, sender *webrtc.RTPSender) Sender {
	ctx, cancel := context.WithCancel(ctx)
	s := &WebRTCSender{
		id:      id,
		ctx:     ctx,
		cancel:  cancel,
		payload: sender.Track().Codec().PayloadType,
		router:  router,
		sender:  sender,
		track:   sender.Track(),
	}

	go s.receiveRTCP()

	return s
}

func (s *WebRTCSender) ID() string {
	return s.id
}

// WriteRTP to the track
func (s *WebRTCSender) WriteRTP(pkt *rtp.Packet) {
	if s.ctx.Err() != nil || s.muted.get() {
		return
	}
	if s.reSync.get() {
		if s.track.Kind() == webrtc.RTPCodecTypeVideo {
			recv := s.router.GetReceiver(0)
			if recv == nil {
				return
			}
			// Forward pli to request a keyframe at max 1 pli per second
			if time.Now().Sub(s.lastPli) > time.Second {
				if err := recv.WriteRTCP(&rtcp.PictureLossIndication{SenderSSRC: pkt.SSRC, MediaSSRC: pkt.SSRC}); err == nil {
					s.lastPli = time.Now()
				}
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

func (s *WebRTCSender) Mute(val bool) {
	if s.muted.get() == val {
		return
	}
	s.muted.set(val)
	if val {
		s.reSync.set(val)
	}
}

func (s *WebRTCSender) Kind() webrtc.RTPCodecType {
	return s.track.Kind()
}

func (s *WebRTCSender) CurrentSpatialLayer() uint8 {
	return 0
}

func (s *WebRTCSender) SwitchSpatialLayer(layer uint8) {
	log.Warnf("can't change layers in simple senders, current: %d target: %d", 0, layer)
}

func (s *WebRTCSender) SwitchTemporalLayer(layer uint8) {
	log.Warnf("can't change layers in simple senders, target: %d", layer)
}

// Close track
func (s *WebRTCSender) Close() {
	s.once.Do(s.close)
}

func (s *WebRTCSender) close() {
	s.cancel()
	if s.onCloseHandler != nil {
		s.onCloseHandler()
	}
}

// OnCloseHandler method to be called on remote tracked removed
func (s *WebRTCSender) OnCloseHandler(fn func()) {
	s.onCloseHandler = fn
}

func (s *WebRTCSender) receiveRTCP() {
	for {
		pkts, err := s.sender.ReadRTCP()
		if err == io.ErrClosedPipe || s.ctx.Err() != nil {
			// Remove sender from receiver
			if recv := s.router.GetReceiver(0); recv != nil {
				recv.DeleteSender(s.id)
			}
			s.Close()
			return
		}

		if err != nil {
			log.Errorf("rtcp err => %v", err)
		}

		for _, pkt := range pkts {
			recv := s.router.GetReceiver(0)
			if recv == nil {
				continue
			}
			switch pkt := pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				if err := recv.WriteRTCP(pkt); err != nil {
					log.Errorf("writing RTCP err %v", err)
				}
			case *rtcp.TransportLayerNack:
				log.Tracef("router got nack: %+v", pkt)
				for _, pair := range pkt.Nacks {
					bufferPkt := recv.GetPacket(pair.PacketID)
					if bufferPkt != nil {
						// We found the packet in the buffer, resend to sub
						s.WriteRTP(bufferPkt)
						continue
					}
					// Packet not found, request from receivers
					nack := &rtcp.TransportLayerNack{
						// origin ssrc
						SenderSSRC: pkt.SenderSSRC,
						MediaSSRC:  pkt.MediaSSRC,
						Nacks:      []rtcp.NackPair{{PacketID: pair.PacketID}},
					}
					if err := recv.WriteRTCP(nack); err != nil {
						log.Errorf("writing nack RTCP err %v", err)
					}
				}
			default:
				// TODO: Use fb packets for congestion control
			}
		}
	}
}

func (s *WebRTCSender) stats() string {
	return fmt.Sprintf("payload: %d | remb: %dkbps", s.track.PayloadType(), s.target/1000)
}
