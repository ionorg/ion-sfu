package buffer

import (
	"sync"
	"testing"

	"github.com/pion/ion-sfu/pkg/logger"
	"github.com/pion/rtcp"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

func CreateTestPacket(pktStamp *SequenceNumberAndTimeStamp) *rtp.Packet {
	if pktStamp == nil {
		return &rtp.Packet{
			Header:  rtp.Header{},
			Raw:     []byte{1, 2, 3},
			Payload: []byte{1, 2, 3},
		}
	}

	return &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: pktStamp.SequenceNumber,
			Timestamp:      pktStamp.Timestamp,
		},
		Raw:     []byte{1, 2, 3},
		Payload: []byte{1, 2, 3},
	}
}

type SequenceNumberAndTimeStamp struct {
	SequenceNumber uint16
	Timestamp      uint32
}

func CreateTestListPackets(snsAndTSs []SequenceNumberAndTimeStamp) (packetList []*rtp.Packet) {
	for _, item := range snsAndTSs {
		item := item
		packetList = append(packetList, CreateTestPacket(&item))
	}

	return packetList
}

func TestNack(t *testing.T) {
	pool := &sync.Pool{
		New: func() interface{} {
			return make([]byte, 1500)
		},
	}
	logger.SetGlobalOptions(logger.GlobalConfig{V: 1}) // 2 - TRACE
	logger := logger.New()
	buff := NewBuffer(123, pool, pool, logger)
	buff.codecType = webrtc.RTPCodecTypeVideo
	assert.NotNil(t, buff)
	var wg sync.WaitGroup
	// 3 nacks 1 Pli
	wg.Add(4)
	buff.OnFeedback(func(fb []rtcp.Packet) {
		for _, pkt := range fb {
			switch p := pkt.(type) {
			case *rtcp.TransportLayerNack:
				if p.Nacks[0].PacketList()[0] == 2 && p.MediaSSRC == 123 {
					wg.Done()
				}
			case *rtcp.PictureLossIndication:
				if p.MediaSSRC == 123 {
					wg.Done()
				}
			}
		}
	})
	buff.Bind(webrtc.RTPParameters{
		HeaderExtensions: nil,
		Codecs: []webrtc.RTPCodecParameters{
			{
				RTPCodecCapability: webrtc.RTPCodecCapability{
					MimeType:  "video/vp8",
					ClockRate: 90000,
					RTCPFeedback: []webrtc.RTCPFeedback{{
						Type: "nack",
					}},
				},
				PayloadType: 96,
			},
		},
	}, Options{})
	for i := 0; i < 15; i++ {
		if i == 2 {
			continue
		}
		pkt := rtp.Packet{
			Header:  rtp.Header{SequenceNumber: uint16(i), Timestamp: uint32(i)},
			Payload: []byte{0xff, 0xff, 0xff, 0xfd, 0xb4, 0x9f, 0x94, 0x1},
		}
		b, err := pkt.Marshal()
		assert.NoError(t, err)
		_, err = buff.Write(b)
		assert.NoError(t, err)
	}
	wg.Wait()
}

func TestNewBuffer(t *testing.T) {
	type args struct {
		options Options
		ssrc    uint32
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "Must not be nil and add packets in sequence",
			args: args{
				options: Options{
					MaxBitRate: 1e6,
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var TestPackets = []*rtp.Packet{
				{
					Header: rtp.Header{
						SequenceNumber: 65533,
					},
				},
				{
					Header: rtp.Header{
						SequenceNumber: 65534,
					},
				},
				{
					Header: rtp.Header{
						SequenceNumber: 2,
					},
				},
				{
					Header: rtp.Header{
						SequenceNumber: 65535,
					},
				},
			}
			pool := &sync.Pool{
				New: func() interface{} {
					return make([]byte, 1500)
				},
			}
			logger.SetGlobalOptions(logger.GlobalConfig{V: 2}) // 2 - TRACE
			logger := logger.New()
			buff := NewBuffer(123, pool, pool, logger)
			buff.codecType = webrtc.RTPCodecTypeVideo
			assert.NotNil(t, buff)
			assert.NotNil(t, TestPackets)
			buff.OnFeedback(func(_ []rtcp.Packet) {
			})
			buff.Bind(webrtc.RTPParameters{
				HeaderExtensions: nil,
				Codecs: []webrtc.RTPCodecParameters{{
					RTPCodecCapability: webrtc.RTPCodecCapability{
						MimeType:     "video/vp8",
						ClockRate:    9600,
						RTCPFeedback: nil,
					},
					PayloadType: 0,
				}},
			}, Options{})

			for _, p := range TestPackets {
				buf, _ := p.Marshal()
				buff.Write(buf)
			}
			// assert.Equal(t, 6, buff.PacketQueue.size)
			assert.Equal(t, uint32(1<<16), buff.cycles)
			assert.Equal(t, uint16(2), buff.maxSeqNo)
		})
	}
}
