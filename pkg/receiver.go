package sfu

import (
	"io"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/rtc/plugins"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v2"
)

var (
	maxSize = 100
)

// Receiver defines a interface for a track receiver
type Receiver interface {
	Track() *webrtc.Track
	ReadRTP() (*rtp.Packet, error)
	ReadRTCP() (rtcp.Packet, error)
	WriteRTCP([]rtcp.Packet) error
	Close()
}

// AudioReceiver receives a video track
type AudioReceiver struct {
	track  *webrtc.Track
	stop   bool
	rtpCh  chan *rtp.Packet
	rtcpCh chan rtcp.Packet
}

// NewAudioReceiver creates a new video track receiver
func NewAudioReceiver(track *webrtc.Track) *AudioReceiver {
	t := &AudioReceiver{
		track: track,
		rtpCh: make(chan *rtp.Packet, maxSize),
	}

	return t
}

// ReadRTP read rtp packet
func (t *AudioReceiver) ReadRTP() (*rtp.Packet, error) {
	return t.track.ReadRTP()
}

// ReadRTCP read rtp packet
func (t *AudioReceiver) ReadRTCP() (rtcp.Packet, error) {
	return nil, nil
}

// WriteRTCP write rtcp packet
func (t *AudioReceiver) WriteRTCP(pkts []rtcp.Packet) error {
	return nil
}

// Track read rtp packet
func (t *AudioReceiver) Track() *webrtc.Track {
	return t.track
}

// Close track
func (t *AudioReceiver) Close() {
	t.stop = true
}

// VideoReceiver receives a video track
type VideoReceiver struct {
	track        *webrtc.Track
	jitterbuffer *plugins.JitterBuffer
	stop         bool
	rtpCh        chan *rtp.Packet
	rtcpCh       chan rtcp.Packet
}

// NewVideoReceiver creates a new video track receiver
func NewVideoReceiver(track *webrtc.Track) *VideoReceiver {
	t := &VideoReceiver{
		track:  track,
		rtpCh:  make(chan *rtp.Packet, maxSize),
		rtcpCh: make(chan rtcp.Packet, maxSize),
	}

	t.jitterbuffer = plugins.NewJitterBuffer(plugins.JitterBufferConfig{}, t.rtpCh, t.rtcpCh)

	go t.receiveRTP()

	return t
}

// ReadRTP read rtp packet
func (t *VideoReceiver) ReadRTP() (*rtp.Packet, error) {
	rtp, ok := <-t.rtpCh
	if !ok {
		return nil, errChanClosed
	}
	return rtp, nil
}

// ReadRTCP read rtp packet
func (t *VideoReceiver) ReadRTCP() (rtcp.Packet, error) {
	rtcp, ok := <-t.rtcpCh
	if !ok {
		return nil, errChanClosed
	}
	return rtcp, nil
}

// WriteRTCP write rtcp packet. WriteRTCP intercepts rtcp packets from the
// sub and either handles them or forwards them back to the publisher.
func (t *VideoReceiver) WriteRTCP(pkts []rtcp.Packet) error {
	for _, pkt := range pkts {
		switch pkt := pkt.(type) {
		case *rtcp.TransportLayerNack:
			log.Infof("Router got nack: %+v", pkt)
			for _, pair := range pkt.Nacks {
				bufferpkt := t.jitterbuffer.GetPacket(pkt.MediaSSRC, pair.PacketID)
				if bufferpkt != nil {
					// We found the packet in the buffer, resend
					t.rtpCh <- bufferpkt
					continue
				}

				nack := &rtcp.TransportLayerNack{
					//origin ssrc
					SenderSSRC: pkt.SenderSSRC,
					MediaSSRC:  pkt.MediaSSRC,
					Nacks:      []rtcp.NackPair{{PacketID: pair.PacketID}},
				}
				t.rtcpCh <- nack
			}
		default:
			t.rtcpCh <- pkt
		}
	}

	return nil
}

// Track read rtp packet
func (t *VideoReceiver) Track() *webrtc.Track {
	return t.track
}

// Close track
func (t *VideoReceiver) Close() {
	t.stop = true
}

// receiveRTP receive all incoming tracks' rtp and sent to one channel
func (t *VideoReceiver) receiveRTP() {
	for {
		if t.stop {
			return
		}

		rtp, err := t.track.ReadRTP()
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Errorf("rtp err => %v", err)
		}
		err = t.jitterbuffer.WriteRTP(rtp)
	}
}
