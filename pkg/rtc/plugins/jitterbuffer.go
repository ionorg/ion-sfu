package plugins

import (
	"errors"
	"fmt"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

const (
	// bandwidth range(kbps)
	minBandwidth = 200
	maxREMBCycle = 5
	maxPLICycle  = 5
)

// JitterBufferConfig .
type JitterBufferConfig struct {
	On            bool `mapstructure:"on"`
	TCCOn         bool `mapstructure:"tccon"`
	REMBCycle     int  `mapstructure:"rembcycle"`
	PLICycle      int  `mapstructure:"plicycle"`
	MaxBandwidth  int  `mapstructure:"maxbandwidth"`
	MaxBufferTime int  `mapstructure:"maxbuffertime"`
}

// JitterBuffer core buffer module
type JitterBuffer struct {
	buffers   map[uint32]*Buffer
	stop      bool
	bandwidth uint64
	lostRate  float64

	id          string
	config      JitterBufferConfig
	outRTPChan  chan *rtp.Packet
	outRTCPChan chan rtcp.Packet
}

// NewJitterBuffer return new JitterBuffer
func NewJitterBuffer(config JitterBufferConfig, outRTPChan chan *rtp.Packet, outRTCPChan chan rtcp.Packet) *JitterBuffer {
	j := &JitterBuffer{
		buffers:     make(map[uint32]*Buffer),
		outRTPChan:  outRTPChan,
		outRTCPChan: outRTCPChan,
	}
	j.init(config)
	j.rembLoop()
	j.pliLoop()
	return j
}

func (j *JitterBuffer) init(config JitterBufferConfig) {
	j.config = config
	log.Infof("JitterBuffer.init j.config=%+v", j.config)
	if j.config.REMBCycle > maxREMBCycle {
		j.config.REMBCycle = maxREMBCycle
	}

	if j.config.PLICycle > maxPLICycle {
		j.config.PLICycle = maxPLICycle
	}

	if j.config.MaxBandwidth < minBandwidth {
		j.config.MaxBandwidth = minBandwidth
	}

	log.Infof("JitterBuffer.init ok  j.config=%v", j.config)
}

// AddBuffer add a buffer by ssrc
func (j *JitterBuffer) AddBuffer(ssrc uint32) *Buffer {
	log.Infof("JitterBuffer.AddBuffer ssrc=%d", ssrc)
	o := BufferOptions{
		TCCOn:      j.config.TCCOn,
		BufferTime: j.config.MaxBufferTime,
	}
	b := NewBuffer(o)
	j.buffers[ssrc] = b
	j.rtcpLoop(b)
	return b
}

// GetBuffer get a buffer by ssrc
func (j *JitterBuffer) GetBuffer(ssrc uint32) *Buffer {
	return j.buffers[ssrc]
}

// GetBuffers get all buffers
func (j *JitterBuffer) GetBuffers() map[uint32]*Buffer {
	return j.buffers
}

// WriteRTP push rtp packet which from pub
func (j *JitterBuffer) WriteRTP(pkt *rtp.Packet) error {
	ssrc := pkt.SSRC
	pt := pkt.PayloadType

	buffer := j.GetBuffer(ssrc)
	if buffer == nil {
		buffer = j.AddBuffer(ssrc)
		log.Infof("JitterBuffer.WriteRTP buffer.SetSSRCPT(%d,%d)", ssrc, pt)
		buffer.SetSSRCPT(ssrc, pt)
	}

	if buffer == nil {
		return errors.New("buffer is nil")
	}

	buffer.Push(pkt)
	j.outRTPChan <- pkt
	return nil
}

func (j *JitterBuffer) rtcpLoop(b *Buffer) {
	go func() {
		for pkt := range b.GetRTCPChan() {
			if j.stop {
				return
			}
			if j.outRTCPChan == nil {
				continue
			}
			j.outRTCPChan <- pkt
		}
	}()
}

func (j *JitterBuffer) rembLoop() {
	go func() {
		for {
			if j.stop {
				return
			}

			if j.config.REMBCycle <= 0 {
				time.Sleep(time.Second)
				continue
			}

			time.Sleep(time.Duration(j.config.REMBCycle) * time.Second)
			for _, buffer := range j.GetBuffers() {
				// only calc video recently
				j.lostRate, j.bandwidth = buffer.GetLostRateBandwidth(uint64(j.config.REMBCycle))
				var bw uint64
				if j.lostRate == 0 && j.bandwidth == 0 {
					bw = uint64(j.config.MaxBandwidth)
				} else if j.lostRate >= 0 && j.lostRate < 0.1 {
					bw = uint64(j.bandwidth * 2)
				} else {
					bw = uint64(float64(j.bandwidth) * (1 - j.lostRate))
				}

				if bw < minBandwidth {
					bw = minBandwidth
				}

				if bw > uint64(j.config.MaxBandwidth) {
					bw = uint64(j.config.MaxBandwidth)
				}

				remb := &rtcp.ReceiverEstimatedMaximumBitrate{
					SenderSSRC: buffer.GetSSRC(),
					Bitrate:    bw * 1000,
					SSRCs:      []uint32{buffer.GetSSRC()},
				}

				j.outRTCPChan <- remb
			}
		}
	}()
}

func (j *JitterBuffer) pliLoop() {
	go func() {
		for {
			if j.stop {
				return
			}

			if j.config.PLICycle <= 0 {
				time.Sleep(time.Second)
				continue
			}
			time.Sleep(time.Duration(j.config.PLICycle) * time.Second)
			for _, buffer := range j.GetBuffers() {
				pli := &rtcp.PictureLossIndication{SenderSSRC: buffer.GetSSRC(), MediaSSRC: buffer.GetSSRC()}
				// log.Infof("pliLoop send pli=%d pt=%v", buffer.GetSSRC(), buffer.GetPayloadType())

				j.outRTCPChan <- pli
			}
		}
	}()
}

// GetPacket get packet from buffer
func (j *JitterBuffer) GetPacket(ssrc uint32, sn uint16) *rtp.Packet {
	buffer := j.buffers[ssrc]
	if buffer == nil {
		return nil
	}
	return buffer.GetPacket(sn)
}

// Stop stop all buffer
func (j *JitterBuffer) Stop() {
	if j.stop {
		return
	}
	j.stop = true
	for _, buffer := range j.buffers {
		buffer.Stop()
	}
	j.buffers = nil
}

// Stat get stat from buffers
func (j *JitterBuffer) Stat() string {
	out := ""
	for ssrc, buffer := range j.buffers {
		out += fmt.Sprintf("ssrc:%d payload:%d | lostRate:%.2f | bandwidth:%dkbps | %s", ssrc, buffer.GetPayloadType(), j.lostRate, j.bandwidth, buffer.GetStat())
	}
	return out
}
