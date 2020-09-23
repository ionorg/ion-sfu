package sfu

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pion/rtp/codecs"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// WebRTCSimulcastSender represents a Sender which writes RTP to a webrtc track
type WebRTCSimulcastSender struct {
	id             string
	ctx            context.Context
	cancel         context.CancelFunc
	router         *Router
	sender         *webrtc.RTPSender
	track          *webrtc.Track
	rtcpCh         chan rtcp.Packet
	target         uint64
	maxBitrate     uint64
	onCloseHandler func()

	simulcastSSRC uint32
	currentLayer  uint8
	targetLayer   uint8
	tsOffset      uint32
	snOffset      uint16
	lastPli       time.Time
	lTSCalc       time.Time
	lSSRC         uint32
	lTS           uint32
	lSN           uint16

	once sync.Once
}

// NewWebRTCSimulcastSender creates a new track sender instance
func NewWebRTCSimulcastSender(ctx context.Context, id string, router *Router, sender *webrtc.RTPSender, layer uint8) Sender {
	ctx, cancel := context.WithCancel(ctx)
	s := &WebRTCSimulcastSender{
		id:            id,
		ctx:           ctx,
		cancel:        cancel,
		router:        router,
		sender:        sender,
		track:         sender.Track(),
		rtcpCh:        make(chan rtcp.Packet, maxSize),
		currentLayer:  layer,
		targetLayer:   layer,
		maxBitrate:    routerConfig.MaxBandwidth * 1000,
		simulcastSSRC: sender.Track().SSRC(),
	}

	go s.receiveRTCP()
	return s
}

func (s *WebRTCSimulcastSender) ID() string {
	return s.id
}

// ReadRTCP read rtp packet
func (s *WebRTCSimulcastSender) ReadRTCP() chan rtcp.Packet {
	return s.rtcpCh
}

// WriteRTP to the track
func (s *WebRTCSimulcastSender) WriteRTP(pkt *rtp.Packet) {
	// Simulcast write RTP is sync, so the packet can be safely modified and restored
	if s.ctx.Err() != nil {
		return
	}
	// Check if packet SSRC is different from before
	// if true, the video source changed
	if s.lSSRC != pkt.SSRC {
		recv := s.router.GetReceiver(s.targetLayer)
		if recv == nil || recv.Track().SSRC() != pkt.SSRC {
			return
		}
		//Forward pli to request a keyframe at max 1 pli per second
		if time.Now().Sub(s.lastPli) > time.Second {
			if err := recv.WriteRTCP(&rtcp.PictureLossIndication{SenderSSRC: pkt.SSRC, MediaSSRC: pkt.SSRC}); err == nil {
				s.lastPli = time.Now()
			}
		}
		relay := false
		// Wait for a keyframe to sync new source
		switch s.track.Codec().PayloadType {
		case webrtc.DefaultPayloadTypeVP8:
			vp8Packet := codecs.VP8Packet{}
			if _, err := vp8Packet.Unmarshal(pkt.Payload); err == nil {
				relay = vp8Packet.Payload[0]&0x01 == 0
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
			return
		}
		// Switch is done update current layer
		s.currentLayer = s.targetLayer
	}
	// Backup pkt original data
	origPT := pkt.Header.PayloadType
	origSSRC := pkt.SSRC
	origSeq := pkt.SequenceNumber
	origTS := pkt.Timestamp
	// Compute how much time passed between the old RTP pkt
	// and the current packet, and fix timestamp on source change
	if !s.lTSCalc.IsZero() && s.lSSRC != pkt.SSRC {
		tDiff := time.Now().Sub(s.lTSCalc)
		td := uint32((tDiff.Milliseconds() * 90) / 1000)
		if td == 0 {
			td = 1
		}
		s.tsOffset = pkt.Timestamp - (s.lTS + td)
		s.snOffset = pkt.SequenceNumber - s.lSN - 1
	} else if s.lTSCalc.IsZero() {
		s.lTS = pkt.Timestamp
		s.lSN = pkt.SequenceNumber
	}
	// Update base
	s.lTSCalc = time.Now()
	s.lSSRC = pkt.SSRC
	s.lTS = pkt.Timestamp - s.tsOffset
	s.lSN = pkt.SequenceNumber - s.snOffset
	// Update pkt headers
	pkt.SSRC = s.simulcastSSRC
	pkt.SequenceNumber = s.lSN
	pkt.Timestamp = s.lTS
	// Write packet to client
	err := s.track.WriteRTP(pkt)
	// Reset packet data
	pkt.Timestamp = origTS
	pkt.SSRC = origSSRC
	pkt.SequenceNumber = origSeq
	pkt.PayloadType = origPT
	if err != nil {
		if err == io.ErrClosedPipe {
			return
		}
		log.Errorf("sender.track.WriteRTP err=%v", err)
	}
}

func (s *WebRTCSimulcastSender) SwitchTo(targetLayer uint8) {
	// Don't switch until previous switch is done or canceled
	if s.currentLayer != s.targetLayer {
		return
	}
	s.targetLayer = targetLayer
	if ok := s.router.SwitchSpatialLayer(s.currentLayer, targetLayer, s); !ok {
		s.targetLayer = s.currentLayer
	}
}

func (s *WebRTCSimulcastSender) CurrentLayer() uint8 {
	return s.currentLayer
}

// OnClose is called when the sender is closed
func (s *WebRTCSimulcastSender) OnClose(f func()) {
	s.onCloseHandler = f
}

// Close track
func (s *WebRTCSimulcastSender) Close() {
	s.once.Do(s.close)
}

func (s *WebRTCSimulcastSender) close() {
	s.cancel()
	if s.onCloseHandler != nil {
		s.onCloseHandler()
	}
}

// OnCloseHandler method to be called on remote tracked removed
func (s *WebRTCSimulcastSender) OnCloseHandler(fn func()) {
	s.onCloseHandler = fn
}

func (s *WebRTCSimulcastSender) receiveRTCP() {
	for {
		pkts, err := s.sender.ReadRTCP()
		if err == io.ErrClosedPipe || s.ctx.Err() != nil {
			close(s.rtcpCh)
			s.Close()
			return
		}

		if err != nil {
			log.Errorf("rtcp err => %v", err)
			continue
		}

		for _, pkt := range pkts {
			recv := s.router.GetReceiver(s.currentLayer)
			if recv == nil {
				continue
			}
			switch pkt := pkt.(type) {
			case *rtcp.PictureLossIndication:
				pkt.MediaSSRC = s.lSSRC
				pkt.SenderSSRC = s.lSSRC
				if err := recv.WriteRTCP(pkt); err != nil {
					log.Errorf("writing RTCP err %v", err)
				}
			case *rtcp.FullIntraRequest:
				pkt.MediaSSRC = s.lSSRC
				pkt.SenderSSRC = s.lSSRC
				if err := recv.WriteRTCP(pkt); err != nil {
					log.Errorf("writing RTCP err %v", err)
				}
			case *rtcp.TransportLayerNack:
				pkt.MediaSSRC = s.lSSRC
				pkt.SenderSSRC = s.lSSRC
				if err := recv.WriteRTCP(pkt); err != nil {
					log.Errorf("writing RTCP err %v", err)
				}
			default:
				// TODO: Use fb packets for congestion control
			}
		}
	}
}

func (s *WebRTCSimulcastSender) stats() string {
	return fmt.Sprintf("payload: %d | remb: %dkbps", s.track.PayloadType(), s.target/1000)
}
