package buffer

import (
	"io"
	"sync"

	"github.com/pion/transport/packetio"
)

type Factory struct {
	sync.RWMutex
	videoPool   *sync.Pool
	audioPool   *sync.Pool
	rtpBuffers  map[uint32]*Buffer
	rtcpReaders map[uint32]*RTCPReader
}

func NewBufferFactory() *Factory {
	return &Factory{
		videoPool: &sync.Pool{
			New: func() interface{} {
				// Make a 2MB buffer for video
				return NewBucket(2*1000*1000, true)
			},
		},
		audioPool: &sync.Pool{
			New: func() interface{} {
				// Make a max 25 packets buffer for audio
				return NewBucket(maxPktSize*25, false)
			},
		},
		rtpBuffers:  make(map[uint32]*Buffer),
		rtcpReaders: make(map[uint32]*RTCPReader),
	}
}

func (f *Factory) GetOrNew(packetType packetio.BufferPacketType, ssrc uint32) io.ReadWriteCloser {
	f.Lock()
	defer f.Unlock()
	switch packetType {
	case packetio.RTCPBufferPacket:
		if reader, ok := f.rtcpReaders[ssrc]; ok {
			return reader
		}
		reader := NewRTCPReader(ssrc)
		f.rtcpReaders[ssrc] = reader
		reader.OnClose(func() {
			f.Lock()
			delete(f.rtcpReaders, ssrc)
			f.Unlock()
		})
		return reader
	case packetio.RTPBufferPacket:
		if reader, ok := f.rtpBuffers[ssrc]; ok {
			return reader
		}
		buffer := NewBuffer(ssrc, f.videoPool, f.audioPool)
		f.rtpBuffers[ssrc] = buffer
		buffer.OnClose(func() {
			f.Lock()
			delete(f.rtpBuffers, ssrc)
			f.Unlock()
		})
		return buffer
	}
	return nil
}

func (f *Factory) GetBufferPair(ssrc uint32) (*Buffer, *RTCPReader) {
	f.RLock()
	defer f.RUnlock()
	return f.rtpBuffers[ssrc], f.rtcpReaders[ssrc]
}

func (f *Factory) GetRTCPReader(ssrc uint32) *RTCPReader {
	f.RLock()
	defer f.RUnlock()
	return f.rtcpReaders[ssrc]
}
