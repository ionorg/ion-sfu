package sfu

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// Sender defines a interface for a track receivers
type Sender interface {
	ID() string
	ReadRTCP() chan rtcp.Packet
	WriteRTP(*rtp.Packet)
	Close()
	OnCloseHandler(fn func())
	CurrentLayer() uint8
	stats() string
	// Simulcast events
	SwitchTo(layer uint8)
}

// WebRTCSender represents a Sender which writes RTP to a webrtc track
type WebRTCSender struct {
	id             string
	ctx            context.Context
	cancel         context.CancelFunc
	sender         *webrtc.RTPSender
	track          *webrtc.Track
	router         *Router
	rtcpCh         chan rtcp.Packet
	sendCh         chan *rtp.Packet
	maxBitrate     uint64
	target         uint64
	currentLayer   uint8
	onCloseHandler func()

	once sync.Once
}

// NewWebRTCSender creates a new track sender instance
func NewWebRTCSender(ctx context.Context, id string, router *Router, sender *webrtc.RTPSender) Sender {
	ctx, cancel := context.WithCancel(ctx)
	sender.Track()
	s := &WebRTCSender{
		id:         id,
		ctx:        ctx,
		cancel:     cancel,
		router:     router,
		sender:     sender,
		track:      sender.Track(),
		maxBitrate: routerConfig.MaxBandwidth * 1000,
		rtcpCh:     make(chan rtcp.Packet, maxSize),
		sendCh:     make(chan *rtp.Packet, maxSize),
	}

	go s.receiveRTCP()
	go s.sendRTP()

	return s
}

func (s *WebRTCSender) ID() string {
	return s.id
}

func (s *WebRTCSender) sendRTP() {
	// There exists a bug in chrome where setLocalDescription
	// fails if track RTP arrives before the sfu offer is set.
	// We delay sending RTP here to avoid the issue.
	// https://bugs.chromium.org/p/webrtc/issues/detail?id=10139
	time.Sleep(500 * time.Millisecond)

	for {
		select {
		case pkt := <-s.sendCh:
			// Transform payload type
			pt := s.track.Codec().PayloadType
			newPkt := *pkt
			newPkt.Header.PayloadType = pt
			pkt = &newPkt

			if err := s.track.WriteRTP(pkt); err != nil {
				if err == io.ErrClosedPipe {
					return
				}
				log.Errorf("sender.track.WriteRTP err=%v", err)
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// ReadRTCP read rtp packet
func (s *WebRTCSender) ReadRTCP() chan rtcp.Packet {
	return s.rtcpCh
}

// WriteRTP to the track
func (s *WebRTCSender) WriteRTP(pkt *rtp.Packet) {
	if s.ctx.Err() == nil {
		s.sendCh <- pkt
	}
}

func (s *WebRTCSender) CurrentLayer() uint8 {
	return s.currentLayer
}

func (s *WebRTCSender) SwitchTo(layer uint8) {
	log.Warnf("can't change layers in simple senders, current: %d target: %d", s.currentLayer, layer)
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
			close(s.rtcpCh)
			s.Close()
			return
		}

		if err != nil {
			log.Errorf("rtcp err => %v", err)
		}

		for _, pkt := range pkts {
			switch pkt := pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				if err := s.router.GetReceiver(0).WriteRTCP(pkt); err != nil {
					log.Errorf("writing RTCP err %v", err)
				}
			case *rtcp.TransportLayerNack:
				log.Tracef("Router got nack: %+v", pkt)
				recv := s.router.GetReceiver(0)
				for _, pair := range pkt.Nacks {
					bufferPkt := recv.GetPacket(pair.PacketID)
					if bufferPkt != nil {
						// We found the packet in the buffer, resend to sub
						s.sendCh <- bufferPkt
						continue
					}
					if routerConfig.MaxNackTime > 0 {
						ln := atomic.LoadInt64(&s.router.lastNack)
						if (time.Now().Unix() - ln) < routerConfig.MaxNackTime {
							continue
						}
						atomic.StoreInt64(&s.router.lastNack, time.Now().Unix())
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
