package buffer

import (
	"sync"
	"sync/atomic"

	log "github.com/pion/ion-log"

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
	return interceptor.RTPReaderFunc(func(bytes []byte, attributes interceptor.Attributes) (int, interceptor.Attributes, error) {
		buffer := i.getBuffer(info.SSRC)
		if buffer == nil {
			buffer = i.newBuffer(info)
		}

		p, att, err := reader.Read(bytes, attributes)
		if err != nil {
			return 0, nil, err
		}
		buffer.push(bytes)
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
	return interceptor.RTCPReaderFunc(func(bytes []byte, attributes interceptor.Attributes) (int, interceptor.Attributes, error) {
		j, attributes, err := reader.Read(bytes, attributes)

		if err != nil {
			return 0, nil, err
		}

		pkts, err := rtcp.Unmarshal(bytes[:j])
		if err != nil {
			return j, nil, err
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

		return j, attributes, nil
	})
}

func (i *Interceptor) BindRTCPWriter(writer interceptor.RTCPWriter) interceptor.RTCPWriter {
	i.twcc.onFeedback = func(pkts []rtcp.Packet) {
		if _, err := writer.Write(pkts, nil); err != nil {
			log.Errorf("Writing buffer twcc rtcp err: %v", err)
		}
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
		pktRaw, err := buffer.getPacket(seq + snOffset)
		if err != nil || pktRaw == nil {
			continue
		}
		pkt := rtp.Packet{}
		if err = pkt.Unmarshal(pktRaw); err != nil {
			log.Errorf("rtp marshal err => %v", err)
			continue
		}

		pkt.SSRC = mediaSSRC
		pkt.SequenceNumber -= snOffset
		pkt.Timestamp -= tsOffset
		pkts = append(pkts, pkt)
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
			if _, err := p.Write(pkts, nil); err != nil {
				log.Errorf("Writing buffer rtcp err: %v", err)
			}
		}
	})
	buffer.onTransportWideCC(func(sn uint16, timeNS int64, marker bool) {
		i.twcc.push(sn, timeNS, marker)
	})
	buffer.onNack(func(pairs []rtcp.NackPair) {
		nack := &rtcp.TransportLayerNack{
			SenderSSRC: buffer.mediaSSRC,
			MediaSSRC:  buffer.mediaSSRC,
			Nacks:      pairs,
		}
		if p, ok := i.rtcpWriter.Load().(interceptor.RTCPWriter); ok {
			if _, err := p.Write([]rtcp.Packet{nack}, nil); err != nil {
				log.Errorf("Writing buffer rtcp err: %v", err)
			}
		}
	})
	i.Lock()
	i.buffers = append(i.buffers, buffer)
	i.twcc.mSSRC = info.SSRC
	i.Unlock()
	return buffer
}
