package buffer

import (
	"sync/atomic"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

type Interceptor struct {
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

func (i *Interceptor) BindRemoteStream(s *interceptor.StreamInfo, reader interceptor.RTPReader) interceptor.RTPReader {
	return interceptor.RTPReaderFunc(func() (*rtp.Packet, interceptor.Attributes, error) {
		var buffer *Buffer
		for _, b := range i.buffers {
			if b.mediaSSRC == s.SSRC {
				buffer = b
				break
			}
		}
		if buffer == nil {
			buffer = NewBuffer(s, Options{})
			buffer.onFeedback(func(pkts []rtcp.Packet) {
				if p, ok := i.rtcpWriter.Load().(interceptor.RTCPWriter); ok {
					p.Write(pkts, nil)
				}
			})
			buffer.onTransportWideCC(func(sn uint16, timeNS int64, marker bool) {
				i.twcc.push(sn, timeNS, marker)
			})
			i.buffers = append(i.buffers, buffer)
			i.twcc.mSSRC = s.SSRC
		}

		p, att, err := reader.Read()
		if err != nil {
			return nil, nil, err
		}
		buffer.push(p)
		return p, att, nil
	})
}

func (i *Interceptor) BindRTCPReader(reader interceptor.RTCPReader) interceptor.RTCPReader {
	return interceptor.RTCPReaderFunc(func() ([]rtcp.Packet, interceptor.Attributes, error) {
		var buffer *Buffer
		pkts, attributes, err := reader.Read()
		if err != nil {
			return nil, nil, err
		}

		for _, pkt := range pkts {
			switch pkt := pkt.(type) {
			case *rtcp.SenderReport:
				if buffer == nil {
					for _, b := range i.buffers {
						if b.mediaSSRC == pkt.SSRC {
							buffer = b
							break
						}
					}
					if buffer == nil {
						return pkts, attributes, nil
					}
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

func (i *Interceptor) GetBufferedPackets(ssrc uint32, snOffset uint16, tsOffset uint32, sn []uint16) []rtp.Packet {
	var buffer *Buffer
	for _, b := range i.buffers {
		if b.mediaSSRC == ssrc {
			buffer = b
			break
		}
	}
	if buffer == nil {
		return nil
	}
	var pkts []rtp.Packet
	for _, seq := range sn {
		h, p, err := buffer.getPacket(seq + snOffset)
		if err != nil {
			continue
		}
		h.SequenceNumber -= snOffset
		h.Timestamp -= tsOffset
		pkts = append(pkts, rtp.Packet{
			Header:  h,
			Payload: p,
		})
	}
	return pkts
}
