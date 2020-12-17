package buffer

import (
	"testing"

	"github.com/pion/interceptor"

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
		info    *interceptor.StreamInfo
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
				info: &interceptor.StreamInfo{
					ID:                  "demo",
					Attributes:          nil,
					SSRC:                1234,
					RTPHeaderExtensions: nil,
					MimeType:            "",
					ClockRate:           9000,
					RTCPFeedback:        nil,
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
			buff := NewBuffer(tt.args.info, tt.args.options)
			buff.codecType = webrtc.RTPCodecTypeVideo
			assert.NotNil(t, buff)
			assert.NotNil(t, TestPackets)

			//for _, p := range TestPackets {
			// buff.push(p)
			//}
			//assert.Equal(t, 6, buff.pktBucket.size)
			assert.Equal(t, uint32(1<<16), buff.cycles)
			assert.Equal(t, uint16(2), buff.maxSeqNo)
		})
	}
}
