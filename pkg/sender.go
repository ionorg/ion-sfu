package sfu

import (
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// Sender defines a interface for a track receiver
type Sender interface {
	ReadRTCP() (rtcp.Packet, error)
	WriteRTP(*rtp.Packet)
	stats() string
	Close()
}

// WebRTCSender represents a Sender which writes RTP to a webrtc track
type WebRTCSender struct {
	mu             sync.RWMutex
	onCloseHandler func()
	track          *webrtc.Track
	stop           bool
	rtcpCh         chan rtcp.Packet
	useRemb        bool
	rembCh         chan *rtcp.ReceiverEstimatedMaximumBitrate
	target         uint64
	sendChan       chan *rtp.Packet
}

// NewWebRTCSender creates a new track sender instance
func NewWebRTCSender(track *webrtc.Track, sender *webrtc.RTPSender) *WebRTCSender {
	s := &WebRTCSender{
		track:    track,
		rtcpCh:   make(chan rtcp.Packet, maxSize),
		rembCh:   make(chan *rtcp.ReceiverEstimatedMaximumBitrate, maxSize),
		sendChan: make(chan *rtp.Packet, maxSize),
	}

	for _, feedback := range track.Codec().RTCPFeedback {
		switch feedback.Type {
		case webrtc.TypeRTCPFBGoogREMB:
			log.Debugf("Using sender feedback %s", webrtc.TypeRTCPFBGoogREMB)
			s.useRemb = true
			go s.rembLoop()
		case webrtc.TypeRTCPFBTransportCC:
			log.Debugf("Using sender feedback %s", webrtc.TypeRTCPFBTransportCC)
			// TODO
		}
	}

	go s.receiveRTCP(sender)
	go s.sendRTP()

	return s
}

func (s *WebRTCSender) sendRTP() {
	for pkt := range s.sendChan {
		s.mu.RLock()
		stop := s.stop
		s.mu.RUnlock()
		if stop {
			break
		}

		// Transform payload type
		pt := s.track.Codec().PayloadType
		newPkt := *pkt
		newPkt.Header.PayloadType = pt
		pkt = &newPkt

		if err := s.track.WriteRTP(pkt); err != nil {
			log.Errorf("wt.WriteRTP err=%v", err)
		}
	}
}

// ReadRTCP read rtp packet
func (s *WebRTCSender) ReadRTCP() (rtcp.Packet, error) {
	rtcp, ok := <-s.rtcpCh
	if !ok {
		return nil, errChanClosed
	}
	return rtcp, nil
}

// WriteRTP to the track
func (s *WebRTCSender) WriteRTP(pkt *rtp.Packet) {
	s.sendChan <- pkt
}

// OnClose is called when the sender is closed
func (s *WebRTCSender) OnClose(f func()) {
	s.onCloseHandler = f
}

// Close track
func (s *WebRTCSender) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop {
		return
	}
	s.stop = true
	close(s.sendChan)
	close(s.rtcpCh)

	if s.onCloseHandler != nil {
		s.onCloseHandler()
	}
}

func (s *WebRTCSender) receiveRTCP(sender *webrtc.RTPSender) {
	for {
		pkts, err := sender.ReadRTCP()
		if err == io.EOF || err == io.ErrClosedPipe {
			return
		}

		if err != nil {
			log.Errorf("rtcp err => %v", err)
		}

		s.mu.RLock()
		stop := s.stop
		s.mu.RUnlock()
		if stop {
			return
		}

		for _, pkt := range pkts {
			switch pkt := pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest, *rtcp.TransportLayerNack:
				s.rtcpCh <- pkt
			case *rtcp.ReceiverEstimatedMaximumBitrate:
				if s.useRemb {
					s.rembCh <- pkt
				}
			default:
			}
		}
	}
}

func (s *WebRTCSender) rembLoop() {
	lastRembTime := time.Now()
	maxRembTime := 200 * time.Millisecond
	rembMin := uint64(100000)
	rembMax := uint64(5000000)
	if rembMin == 0 {
		rembMin = 10000 // 10 KBit
	}
	if rembMax == 0 {
		rembMax = 100000000 // 100 MBit
	}
	var lowest uint64 = math.MaxUint64
	var rembCount, rembTotalRate uint64

	for pkt := range s.rembCh {
		s.mu.RLock()
		stop := s.stop
		s.mu.RUnlock()
		if stop {
			break
		}

		// Update stats
		rembCount++
		rembTotalRate += pkt.Bitrate
		if pkt.Bitrate < lowest {
			lowest = pkt.Bitrate
		}

		// Send upstream if time
		if time.Since(lastRembTime) > maxRembTime {
			lastRembTime = time.Now()
			avg := uint64(rembTotalRate / rembCount)

			_ = avg
			s.target = lowest

			if s.target < rembMin {
				s.target = rembMin
			} else if s.target > rembMax {
				s.target = rembMax
			}

			newPkt := &rtcp.ReceiverEstimatedMaximumBitrate{
				Bitrate:    s.target,
				SenderSSRC: 1,
				SSRCs:      pkt.SSRCs,
			}

			s.rtcpCh <- newPkt

			// Reset stats
			rembCount = 0
			rembTotalRate = 0
			lowest = math.MaxUint64
		}
	}
}

func (s *WebRTCSender) stats() string {
	return fmt.Sprintf("payload: %d | remb: %dkbps", s.track.PayloadType(), s.target/1000)
}
