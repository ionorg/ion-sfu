package sfu

import (
	"testing"

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
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	p, _ := api.NewPeerConnection(webrtc.Configuration{})
	track, _ := p.NewTrack(webrtc.DefaultPayloadTypeVP8, 1234, "test", "pion")

	type args struct {
		track   *webrtc.Track
		rtpChan chan extPacket
		o       BufferOptions
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "Must not be nil and add packets in sequence",
			args: args{
				track:   track,
				rtpChan: make(chan extPacket, 10),
				o: BufferOptions{
					TWCCExt:    0,
					BufferTime: 1e3,
					MaxBitRate: 1e3,
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
					Payload: []byte{0xff, 0xff, 0xff, 0xfd, 0xb4, 0x9f, 0x94, 0x1},
				},
				{
					Header: rtp.Header{
						SequenceNumber: 65534,
					},
					Payload: []byte{0xff, 0xff, 0xff, 0xfd, 0xb4, 0x9f, 0x94, 0x1},
				},
				{
					Header: rtp.Header{
						SequenceNumber: 2,
					},
					Payload: []byte{0xff, 0xff, 0xff, 0xfd, 0xb4, 0x9f, 0x94, 0x1},
				},
				{
					Header: rtp.Header{
						SequenceNumber: 65535,
					},
					Payload: []byte{0xff, 0xff, 0xff, 0xfd, 0xb4, 0x9f, 0x94, 0x1},
				},
			}
			buff := NewBuffer(tt.args.track, tt.args.rtpChan, tt.args.o)
			assert.NotNil(t, buff)

			for _, p := range TestPackets {
				buff.push(p)
			}
			assert.Equal(t, 6, buff.pktQueue.size)
			assert.Equal(t, uint32(1<<16), buff.cycles)
			assert.Equal(t, uint16(2), buff.maxSeqNo)
		})
	}
}
