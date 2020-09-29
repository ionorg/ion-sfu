package sfu

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// Sender defines a interface for a track receivers
type Sender interface {
	ID() string
	WriteRTP(*rtp.Packet)
	CurrentSpatialLayer() uint8
	OnCloseHandler(fn func())
	Close()
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
	maxBitrate     uint64
	target         uint64
	currentLayer   uint8
	onCloseHandler func()

	once sync.Once
}

// NewWebRTCSender creates a new track sender instance
func NewWebRTCSender(ctx context.Context, id string, router Router, sender *webrtc.RTPSender) Sender {
	ctx, cancel := context.WithCancel(ctx)
	s := &WebRTCSender{
		id:     id,
		ctx:    ctx,
		cancel: cancel,
		router: router,
		sender: sender,
		track:  sender.Track(),
	}

	go s.receiveRTCP()

	return s
}

func (s *WebRTCSender) ID() string {
	return s.id
}

// WriteRTP to the track
func (s *WebRTCSender) WriteRTP(pkt *rtp.Packet) {
	if s.ctx.Err() != nil {
		return
	}
	// Transform payload type
	bPt := pkt.PayloadType
	pt := s.track.Codec().PayloadType
	pkt.PayloadType = pt
	err := s.track.WriteRTP(pkt)
	// Restore packet
	pkt.PayloadType = bPt
	if err != nil {
		if err == io.ErrClosedPipe {
			return
		}
		log.Errorf("sender.track.WriteRTP err=%v", err)
	}
}

func (s *WebRTCSender) CurrentSpatialLayer() uint8 {
	return s.currentLayer
}

func (s *WebRTCSender) SwitchSpatialLayer(layer uint8) {
	log.Warnf("can't change layers in simple senders, current: %d target: %d", s.currentLayer, layer)
}

func (s *WebRTCSender) SwitchTemporalLayer(layer uint8) {
	log.Warnf("can't change layers in simple senders, target: %d", layer)
}

// OnClose is called when the sender is closed
func (s *WebRTCSender) OnClose(f func()) {
	s.onCloseHandler = f
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
