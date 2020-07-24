package sfu

import (
	"fmt"
	"io"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

const (
	// bandwidth range(kbps)
	minBandwidth = 200
	maxSize      = 100
)

// Receiver defines a interface for a track receiver
type Receiver interface {
	Track() *webrtc.Track
	GetPacket(sn uint16) *rtp.Packet
	ReadRTP() (*rtp.Packet, error)
	ReadRTCP() (rtcp.Packet, error)
	WriteRTCP(rtcp.Packet) error
	Close()
}

// AudioReceiver receives a video track
type AudioReceiver struct {
	track *webrtc.Track
	stop  bool
	rtpCh chan *rtp.Packet
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
	if t.stop {
		return nil, errReceiverClosed
	}
	return t.track.ReadRTP()
}

// ReadRTCP read rtp packet
func (t *AudioReceiver) ReadRTCP() (rtcp.Packet, error) {
	return nil, errMethodNotSupported
}

// WriteRTCP write rtcp packet
func (t *AudioReceiver) WriteRTCP(pkt rtcp.Packet) error {
	return errMethodNotSupported
}

// Track read rtp packet
func (t *AudioReceiver) Track() *webrtc.Track {
	return t.track
}

// GetPacket returns nil since audio isn't buffered (uses fec)
func (t *AudioReceiver) GetPacket(sn uint16) *rtp.Packet {
	return nil
}

// Close track
func (t *AudioReceiver) Close() {
	t.stop = true
}

// VideoReceiver receives a video track
type VideoReceiver struct {
	buffer    *Buffer
	track     *webrtc.Track
	bandwidth uint64
	lostRate  float64
	stop      bool
	rtpCh     chan *rtp.Packet
	rtcpCh    chan rtcp.Packet

	pliCycle     int
	rembCycle    int
	maxBandwidth int
}

// VideoReceiverConfig .
type VideoReceiverConfig struct {
	TCCOn         bool `mapstructure:"tccon"`
	REMBCycle     int  `mapstructure:"rembcycle"`
	PLICycle      int  `mapstructure:"rembcycle"`
	MaxBandwidth  int  `mapstructure:"maxbandwidth"`
	MaxBufferTime int  `mapstructure:"maxbuffertime"`
}

// NewVideoReceiver creates a new video track receiver
func NewVideoReceiver(config VideoReceiverConfig, track *webrtc.Track) *VideoReceiver {
	v := &VideoReceiver{
		buffer: NewBuffer(track.SSRC(), track.PayloadType(), BufferOptions{
			TCCOn:      config.TCCOn,
			BufferTime: config.MaxBufferTime,
		}),
		track:        track,
		rtpCh:        make(chan *rtp.Packet, maxSize),
		rtcpCh:       make(chan rtcp.Packet, maxSize),
		rembCycle:    config.REMBCycle,
		pliCycle:     config.PLICycle,
		maxBandwidth: config.MaxBandwidth,
	}

	go v.receiveRTP()
	go v.pliLoop()
	go v.rembLoop()
	go v.bufferRtcpLoop()

	return v
}

// ReadRTP read rtp packet
func (v *VideoReceiver) ReadRTP() (*rtp.Packet, error) {
	rtp, ok := <-v.rtpCh
	if !ok {
		return nil, errChanClosed
	}
	return rtp, nil
}

// ReadRTCP read rtp packet
func (v *VideoReceiver) ReadRTCP() (rtcp.Packet, error) {
	rtcp, ok := <-v.rtcpCh
	if !ok {
		return nil, errChanClosed
	}
	return rtcp, nil
}

// WriteRTCP write rtcp packet
func (v *VideoReceiver) WriteRTCP(pkt rtcp.Packet) error {
	v.rtcpCh <- pkt
	return nil
}

// Track read rtp packet
func (v *VideoReceiver) Track() *webrtc.Track {
	return v.track
}

// GetPacket get a buffered packet if we have one
func (v *VideoReceiver) GetPacket(sn uint16) *rtp.Packet {
	return v.buffer.GetPacket(sn)
}

// Close track
func (v *VideoReceiver) Close() {
	v.stop = true
	v.buffer.Stop()
}

// receiveRTP receive all incoming tracks' rtp and sent to one channel
func (v *VideoReceiver) receiveRTP() {
	for {
		if v.stop {
			return
		}

		rtp, err := v.track.ReadRTP()
		log.Tracef("got packet %v", rtp)
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Errorf("rtp err => %v", err)
		}

		v.buffer.Push(rtp)
		v.rtpCh <- rtp

		if err != nil {
			log.Errorf("jb err => %v", err)
		}
	}
}

func (v *VideoReceiver) pliLoop() {
	for {
		if v.stop {
			return
		}

		if v.pliCycle <= 0 {
			time.Sleep(time.Second)
			continue
		}

		time.Sleep(time.Duration(v.pliCycle) * time.Second)
		pli := &rtcp.PictureLossIndication{SenderSSRC: v.track.SSRC(), MediaSSRC: v.track.SSRC()}
		// log.Infof("pliLoop send pli=%d pt=%v", buffer.GetSSRC(), buffer.GetPayloadType())

		v.rtcpCh <- pli
	}
}

func (v *VideoReceiver) bufferRtcpLoop() {
	for pkt := range v.buffer.GetRTCPChan() {
		if v.stop {
			return
		}
		v.rtcpCh <- pkt
	}
}

func (v *VideoReceiver) rembLoop() {
	for {
		if v.stop {
			return
		}

		if v.rembCycle <= 0 {
			time.Sleep(time.Second)
			continue
		}

		time.Sleep(time.Duration(v.rembCycle) * time.Second)
		// only calc video recently
		v.lostRate, v.bandwidth = v.buffer.GetLostRateBandwidth(uint64(v.rembCycle))
		var bw uint64
		if v.lostRate == 0 && v.bandwidth == 0 {
			bw = uint64(v.maxBandwidth)
		} else if v.lostRate >= 0 && v.lostRate < 0.1 {
			bw = uint64(v.bandwidth * 2)
		} else {
			bw = uint64(float64(v.bandwidth) * (1 - v.lostRate))
		}

		if bw < minBandwidth {
			bw = minBandwidth
		}

		if bw > uint64(v.maxBandwidth) {
			bw = uint64(v.maxBandwidth)
		}

		remb := &rtcp.ReceiverEstimatedMaximumBitrate{
			SenderSSRC: v.buffer.GetSSRC(),
			Bitrate:    bw * 1000,
			SSRCs:      []uint32{v.buffer.GetSSRC()},
		}

		v.rtcpCh <- remb
	}
}

// Stat get stat from buffers
func (v *VideoReceiver) stat() string {
	out := ""
	out += fmt.Sprintf("payload:%d | lostRate:%.2f | bandwidth:%dkbps | %s", v.buffer.GetPayloadType(), v.lostRate, v.bandwidth, v.buffer.GetStat())
	return out
}
