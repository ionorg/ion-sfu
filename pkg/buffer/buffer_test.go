package buffer

import (
	"sync"
	"testing"

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
					BufferTime: 1000,
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
					return NewBucket(2*1000*1000, true)
				},
			}
			buff := NewBuffer(123, pool, pool)
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
			//assert.Equal(t, 6, buff.bucket.size)
			assert.Equal(t, uint32(1<<16), buff.cycles)
			assert.Equal(t, uint16(2), buff.maxSeqNo)
		})
	}
}
