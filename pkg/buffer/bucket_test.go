package buffer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

var TestPackets = []*rtp.Packet{
	{
		Header: rtp.Header{
			SequenceNumber: 1,
		},
	},
	{
		Header: rtp.Header{
			SequenceNumber: 3,
		},
	},
	{
		Header: rtp.Header{
			SequenceNumber: 4,
		},
	},
	{
		Header: rtp.Header{
			SequenceNumber: 6,
		},
	},
	{
		Header: rtp.Header{
			SequenceNumber: 7,
		},
	},
	{
		Header: rtp.Header{
			SequenceNumber: 10,
		},
	},
}

func Test_queue_nack(t *testing.T) {
	type fields struct {
		headSN uint16
	}
	tests := []struct {
		name   string
		fields fields
		want   *rtcp.NackPair
	}{
		{
			name:   "Most generate correct pairs packet",
			fields: fields{},
			want: &rtcp.NackPair{
				PacketID:    2,
				LostPackets: 100,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			q := &Bucket{
				headSN: tt.fields.headSN,
			}
			for _, p := range TestPackets {
				diff := p.SequenceNumber - q.headSN
				for i := 1; i < int(diff); i++ {
					q.push(nil)
					q.counter++
				}
				q.headSN = p.SequenceNumber
				q.counter++
				// q.Write(p)
			}
			//if got := q.pairs(); !reflect.DeepEqual(got, tt.want) {
			//	t.Errorf("pairs() = %v, want %v", got, tt.want)
			//}
		})
	}
}

func Test_queue(t *testing.T) {
	q := NewBucket(2 * 1000 * 1000)
	for _, p := range TestPackets {
		p := p
		buf, err := p.Marshal()
		assert.NoError(t, err)
		assert.NotPanics(t, func() {
			q.addPacket(buf, p.SequenceNumber, true)
		})
	}
	var expectedSN uint16
	expectedSN = 6
	np := rtp.Packet{}
	npB := q.getPacket(6)
	err := np.Unmarshal(npB)
	assert.NoError(t, err)
	assert.Equal(t, expectedSN, np.SequenceNumber)

	np2 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 8,
		},
	}
	buf, err := np2.Marshal()
	assert.NoError(t, err)
	expectedSN = 8
	q.addPacket(buf, 8, false)
	npC := q.getPacket(8)
	err = np.Unmarshal(npC)
	assert.NoError(t, err)
	assert.Equal(t, expectedSN, np.SequenceNumber)
}

func Test_queue_edges(t *testing.T) {
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
	}
	q := NewBucket(2 * 1000 * 1000)
	q.headSN = 65532
	q.step = q.maxSteps - 2
	for _, p := range TestPackets {
		p := p
		assert.NotNil(t, p)
		assert.NotPanics(t, func() {
			p := p
			buf, err := p.Marshal()
			assert.NoError(t, err)
			assert.NotPanics(t, func() {
				q.addPacket(buf, p.SequenceNumber, true)
			})
		})
	}
	var expectedSN uint16
	expectedSN = 65534
	np := rtp.Packet{}
	npB := q.getPacket(expectedSN)
	err := np.Unmarshal(npB)
	assert.NoError(t, err)
	assert.Equal(t, expectedSN, np.SequenceNumber)

	np2 := rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 65535,
		},
	}
	buf, err := np2.Marshal()
	assert.NoError(t, err)
	q.addPacket(buf, np2.SequenceNumber, false)
	npC := q.getPacket(expectedSN + 1)
	err = np.Unmarshal(npC)
	assert.NoError(t, err)
	assert.Equal(t, expectedSN+1, np.SequenceNumber)
}
