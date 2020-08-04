package sfu

import (
	"fmt"
	"io"
	"math"
	"net"
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
	track    Track
	stop     bool
	rtcpCh   chan rtcp.Packet
	useRemb  bool
	rembCh   chan *rtcp.ReceiverEstimatedMaximumBitrate
	target   uint64
	sendChan chan *rtp.Packet
}

// NewWebRTCSender creates a new track sender instance
func NewWebRTCSender(track Track, sender *webrtc.RTPSender) *WebRTCSender {
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
		// Transform payload type
		pt := s.track.Codec().PayloadType
		newPkt := *pkt
		newPkt.Header.PayloadType = pt
		pkt = &newPkt

		if err := s.track.WriteRTP(pkt); err != nil {
			log.Errorf("wt.WriteRTP err=%v", err)
		}
	}
	log.Infof("Closing send writer")
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

// Close track
func (s *WebRTCSender) Close() {
	s.stop = true
	close(s.sendChan)
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

		if s.stop {
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
	return fmt.Sprintf("payload:%d | remb: %dkbps", s.track.PayloadType(), s.target/1000)
}

// RTPSender represents a Sender which writes RTP to a webrtc track
type RTPSender struct {
	track     *webrtc.Track
	transport *RTPTransport
	stop      bool
	sendChan  chan *rtp.Packet
}

// NewRTPSender creates a new track sender instance
func NewRTPSender(track *webrtc.Track, addr string) *RTPSender {
	srcAddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	dstAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Errorf("net.ResolveUDPAddr => %s", err.Error())
		return nil
	}
	conn, err := net.DialUDP("udp", srcAddr, dstAddr)
	if err != nil {
		log.Errorf("net.DialUDP => %s", err.Error())
		return nil
	}
	t := NewRTPTransport(conn)
	log.Infof("NewOutRTPTransport %s", addr)

	s := &RTPSender{
		track:     track,
		transport: t,
		sendChan:  make(chan *rtp.Packet, maxSize),
	}

	go s.sendRTP()

	return s
}

func (s *RTPSender) sendRTP() {
	for pkt := range s.sendChan {
		s.WriteRTP(pkt)
	}
	log.Infof("Closing send writer")
}

// ReadRTCP read rtp packet
func (s *RTPSender) ReadRTCP() (rtcp.Packet, error) {
	return nil, nil
}

// WriteRTP to the track
func (s *RTPSender) WriteRTP(pkt *rtp.Packet) {
	s.sendChan <- pkt
}

// Close track
func (s *RTPSender) Close() {
	s.stop = true
	close(s.sendChan)
}

func (s *RTPSender) stats() string {
	return fmt.Sprintf("payload:%d | addr: %s", s.track.PayloadType(), s.transport.RemoteAddr())
}
