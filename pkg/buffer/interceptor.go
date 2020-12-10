package buffer

import (
	"sync"
	"sync/atomic"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

type Interceptor struct {
	sync.RWMutex
	twcc       *TransportWideCC
	buffers    []*Buffer
	rtcpWriter atomic.Value
	interceptor.NoOp
}

func NewBufferInterceptor() *Interceptor {
	return &Interceptor{
		twcc:       newTransportWideCC(),
		buffers:    make([]*Buffer, 0, 4),
		rtcpWriter: atomic.Value{},
	}
}

func (i *Interceptor) BindRemoteStream(info *interceptor.StreamInfo, reader interceptor.RTPReader) interceptor.RTPReader {
	return interceptor.RTPReaderFunc(func() (*rtp.Packet, interceptor.Attributes, error) {
		buffer := i.getBuffer(info.SSRC)
		if buffer == nil {
			buffer = i.newBuffer(info)
		}

		p, att, err := reader.Read()
		if err != nil {
			return nil, nil, err
		}
		buffer.push(p)
		return p, att, nil
	})
}

func (i *Interceptor) UnbindRemoteStream(info *interceptor.StreamInfo) {
	i.Lock()
	idx := -1
	for j, buff := range i.buffers {
		if buff.mediaSSRC == info.SSRC {
			idx = j
			break
		}
	}
	if idx == -1 {
		i.Unlock()
		return
	}
	i.buffers[idx] = i.buffers[len(i.buffers)-1]
	i.buffers[len(i.buffers)-1] = nil
	i.buffers = i.buffers[:len(i.buffers)-1]
	i.Unlock()
}

func (i *Interceptor) BindRTCPReader(reader interceptor.RTCPReader) interceptor.RTCPReader {
	return interceptor.RTCPReaderFunc(func() ([]rtcp.Packet, interceptor.Attributes, error) {
		pkts, attributes, err := reader.Read()
		if err != nil {
			return nil, nil, err
		}

		for _, pkt := range pkts {
			switch pkt := pkt.(type) {
			case *rtcp.SenderReport:
				buffer := i.getBuffer(pkt.SSRC)
				if buffer == nil {
					continue
				}
				buffer.setSenderReportData(pkt.RTPTime, pkt.NTPTime)
			}
		}

		return pkts, attributes, nil
	})
}

func (i *Interceptor) BindRTCPWriter(writer interceptor.RTCPWriter) interceptor.RTCPWriter {
	i.twcc.onFeedback = func(pkts []rtcp.Packet) {
		writer.Write(pkts, nil)
	}
	i.rtcpWriter.Store(writer)
	return writer
}

func (i *Interceptor) GetBufferedPackets(ssrc, mediaSSRC uint32, snOffset uint16, tsOffset uint32, sn []uint16) []rtp.Packet {
	buffer := i.getBuffer(ssrc)
	if buffer == nil {
		return nil
	}
	var pkts []rtp.Packet
	for _, seq := range sn {
		h, p, err := buffer.getPacket(seq + snOffset)
		if err != nil {
			continue
		}
		h.SSRC = mediaSSRC
		h.SequenceNumber -= snOffset
		h.Timestamp -= tsOffset
		pkts = append(pkts, rtp.Packet{
			Header:  h,
			Payload: p,
		})
	}
	return pkts
}

func (i *Interceptor) getBuffer(ssrc uint32) *Buffer {
	i.RLock()
	defer i.RUnlock()
	for _, b := range i.buffers {
		if b.mediaSSRC == ssrc {
			return b
		}
	}
	return nil
}

func (i *Interceptor) newBuffer(info *interceptor.StreamInfo) *Buffer {
	buffer := NewBuffer(info, Options{})
	buffer.onFeedback(func(pkts []rtcp.Packet) {
		if p, ok := i.rtcpWriter.Load().(interceptor.RTCPWriter); ok {
			p.Write(pkts, nil)
		}
	})
	buffer.onTransportWideCC(func(sn uint16, timeNS int64, marker bool) {
		i.twcc.push(sn, timeNS, marker)
	})
	i.Lock()
	i.buffers = append(i.buffers, buffer)
	i.twcc.mSSRC = info.SSRC
	i.Unlock()
	return buffer
}
