package sfu

import (
	"context"
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
	ReadRTCP() chan rtcp.Packet
	WriteRTP(*rtp.Packet)
	Close()
	OnCloseHandler(fn func())
	stats() string
	// Simulcast events
	Switch() chan uint8
	SwitchTo(layer uint8)
}

// WebRTCSender represents a Sender which writes RTP to a webrtc track
type WebRTCSender struct {
	ctx            context.Context
	cancel         context.CancelFunc
	onCloseHandler func()
	sender         *webrtc.RTPSender
	track          *webrtc.Track
	rtcpCh         chan rtcp.Packet
	switchCh       chan uint8
	maxBitrate     uint64
	target         uint64
	sendChan       chan *rtp.Packet

	once sync.Once
}

// NewWebRTCSender creates a new track sender instance
func NewWebRTCSender(ctx context.Context, track *webrtc.Track, sender *webrtc.RTPSender) Sender {
	ctx, cancel := context.WithCancel(ctx)
	s := &WebRTCSender{
		ctx:        ctx,
		cancel:     cancel,
		sender:     sender,
		track:      track,
		maxBitrate: routerConfig.MaxBandwidth * 1000,
		rtcpCh:     make(chan rtcp.Packet, maxSize),
		switchCh:   make(chan uint8, 1),
		sendChan:   make(chan *rtp.Packet, maxSize),
	}

	go s.receiveRTCP()
	go s.sendRTP()

	return s
}

func (s *WebRTCSender) sendRTP() {
	// There exists a bug in chrome where setLocalDescription
	// fails if track RTP arrives before the sfu offer is set.
	// We delay sending RTP here to avoid the issue.
	// https://bugs.chromium.org/p/webrtc/issues/detail?id=10139
	time.Sleep(500 * time.Millisecond)

	for {
		select {
		case pkt := <-s.sendChan:
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
		s.sendChan <- pkt
	}
}

func (s *WebRTCSender) Switch() chan uint8 {
	return s.switchCh
}

func (s *WebRTCSender) SwitchTo(layer uint8) {
	s.switchCh <- layer
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
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest, *rtcp.TransportLayerNack:
				s.rtcpCh <- pkt
			default:
				// TODO: Use fb packets for congestion control
			}
		}
	}
}

func (s *WebRTCSender) stats() string {
	return fmt.Sprintf("payload: %d | remb: %dkbps", s.track.PayloadType(), s.target/1000)
}
