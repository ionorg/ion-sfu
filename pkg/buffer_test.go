package sfu

import (
	"testing"

	"github.com/pion/rtp"
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

func TestBuffer_tsDelta(t *testing.T) {
	type args struct {
		x uint32
		y uint32
	}
	tests := []struct {
		name string
		args args
		want uint32
	}{
		{
			name: "When x is greater than y",
			args: args{
				x: 6,
				y: 1,
			},
			want: uint32(5),
		},
		{
			name: "When y is greater than x",
			args: args{
				x: 3,
				y: 9,
			},
			want: uint32(6),
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := tsDelta(tt.args.x, tt.args.y)
			assert.Equal(t, got, tt.want)
		})
	}
}
