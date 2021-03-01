package buffer

import (
	"sync"
	"testing"

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
	q := NewPacketQueue(&sync.Pool{New: func() interface{} {
		return make([]byte, 1500)
	}}, 500)

	for _, p := range TestPackets {
		p := p
		buf, err := p.Marshal()
		assert.NoError(t, err)
		assert.NotPanics(t, func() {
			q.AddPacket(buf, p.SequenceNumber, true)
		})
	}
	var expectedSN uint16
	expectedSN = 6
	np := rtp.Packet{}
	buff := make([]byte, 1500)
	i, err := q.GetPacket(buff, 6)
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
	q.AddPacket(buf, 8, false)
	i, err = q.GetPacket(buff, expectedSN)
	assert.NoError(t, err)
	err = np.Unmarshal(buff[:i])
	assert.NoError(t, err)
	assert.Equal(t, expectedSN, np.SequenceNumber)
	assert.NotPanics(t, q.Close)
}

func Test_queue_disorder(t *testing.T) {
	d := []byte("dummy data")
	q := NewPacketQueue(&sync.Pool{New: func() interface{} {
		return make([]byte, 1500)
	}}, 500)

	q.headSN = 25745
	q.AddPacket(d, 25746, true)
	dd := q.AddPacket(d, 25743, false)
	assert.NotNil(t, dd)
	assert.Equal(t, dd, d)
	assert.Equal(t, 4, q.size)
	dd = q.AddPacket(d, 25745, false)
	assert.NotNil(t, dd)
	assert.Equal(t, dd, d)
	assert.Equal(t, 4, q.size)
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
	q := NewPacketQueue(&sync.Pool{New: func() interface{} {
		return make([]byte, 1500)
	}}, 500)
	q.headSN = 65532
	for _, p := range TestPackets {
		p := p
		assert.NotNil(t, p)
		assert.NotPanics(t, func() {
			p := p
			buf, err := p.Marshal()
			assert.NoError(t, err)
			assert.NotPanics(t, func() {
				q.AddPacket(buf, p.SequenceNumber, true)
			})
		})
	}
	var expectedSN uint16
	expectedSN = 65534
	np := rtp.Packet{}
	buff := make([]byte, 1500)
	i, err := q.GetPacket(buff, expectedSN)
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
	q.AddPacket(buf, np2.SequenceNumber, false)
	i, err = q.GetPacket(buff, expectedSN+1)
	assert.NoError(t, err)
	err = np.Unmarshal(buff[:i])
	assert.NoError(t, err)
	assert.Equal(t, expectedSN+1, np.SequenceNumber)
	assert.NotPanics(t, q.Close)
}
