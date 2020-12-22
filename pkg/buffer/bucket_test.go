package buffer

import (
	"testing"

	"github.com/pion/rtcp"

	"github.com/stretchr/testify/assert"

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

func Test_queue(t *testing.T) {
	q := NewBucket(2*1000*1000, true)
	q.onLost = func(_ []rtcp.NackPair, _ bool) {
	}

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
	buff := make([]byte, maxPktSize)
	i, err := q.getPacket(buff, 6)
	assert.NoError(t, err)
	err = np.Unmarshal(buff[:i])
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
	i, err = q.getPacket(buff, expectedSN)
	assert.NoError(t, err)
	err = np.Unmarshal(buff[:i])
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
	q := NewBucket(2*1000*1000, true)
	q.onLost = func(_ []rtcp.NackPair, _ bool) {
	}
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
	buff := make([]byte, maxPktSize)
	i, err := q.getPacket(buff, expectedSN)
	assert.NoError(t, err)
	err = np.Unmarshal(buff[:i])
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
	i, err = q.getPacket(buff, expectedSN+1)
	assert.NoError(t, err)
	err = np.Unmarshal(buff[:i])
	assert.NoError(t, err)
	assert.Equal(t, expectedSN+1, np.SequenceNumber)
}
