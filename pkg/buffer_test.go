package sfu

import (
	"strings"
	"testing"

	"github.com/pion/rtcp"
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

func TestBufferWithDefaultBufferTime(t *testing.T) {
	buffer := NewBuffer(nil, 1, 1, BufferOptions{})
	defer buffer.Stop()

	pkt := CreateTestPacket(nil)

	buffer.Push(pkt)
	assert.Equal(t, buffer.GetPayloadType(), uint8(1))
	assert.Equal(t, buffer.GetSSRC(), uint32(1))

	receivedPkt := buffer.GetPacket(0)

	assert.Equal(t, receivedPkt, pkt)

	assert.Nil(t, buffer.GetPacket(1))
	expectedStatString := []string{"buffer", "lastNackSN"}
	for _, entry := range expectedStatString {
		assert.True(t, strings.Contains(buffer.stats(), entry))
	}

}

func TestBufferWithBufferTimeAndZeroSSRC(t *testing.T) {

	buffer := NewBuffer(nil, 0, 0, BufferOptions{
		BufferTime: 10,
	})
	defer buffer.Stop()

	pktsSnsAndTs := []SequenceNumberAndTimeStamp{
		{
			SequenceNumber: 11,
			Timestamp:      123,
		},
		{
			SequenceNumber: 12,
			Timestamp:      124,
		},
		{
			SequenceNumber: 14,
			Timestamp:      200,
		},
		{
			SequenceNumber: 15,
			Timestamp:      200,
		},
		{
			SequenceNumber: 15,
			Timestamp:      400,
		},
	}

	packets := CreateTestListPackets(pktsSnsAndTs)

	for _, packet := range packets {
		buffer.Push(packet)
	}

	nackPair, lostPkt := buffer.GetNackPair(buffer.pktBuffer, 0, 1)
	assert.Equal(t, 1, lostPkt)
	assert.Equal(t, rtcp.NackPair{}, nackPair)

	nackPair, lostPkt = buffer.GetNackPair(buffer.pktBuffer, 0, 25)
	assert.Equal(t, 0, lostPkt)
	assert.Equal(t, rtcp.NackPair{}, nackPair)

	nackPair, lostPkt = buffer.GetNackPair(buffer.pktBuffer, 10, 12)
	assert.Equal(t, 1, lostPkt)
	assert.Equal(t, uint16(10), nackPair.PacketID)

	lostRate, byteRate := buffer.GetLostRateBandwidth(uint64(12))
	byteRate = uint64(byteRate / 1000)
	assert.Equal(t, lostRate, float64(0))
	assert.Equal(t, byteRate, uint64(0))

	buffer.clearOldPkt(9999, 13)
	buffer.clearOldPkt(99999, 14)
	buffer.clearOldPkt(1200, 17)
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
