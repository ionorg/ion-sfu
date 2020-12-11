package stats

import (
	"sync"

	"github.com/pion/interceptor"
	"github.com/pion/ion-sfu/pkg/buffer"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

type Interceptor struct {
	sync.RWMutex
	bufferInterceptor *buffer.Interceptor
	streams           []*Stream
	interceptor.NoOp
}

func NewStreamInterceptor(interceptor *buffer.Interceptor) *Interceptor {
	return &Interceptor{
		bufferInterceptor: interceptor,
	}
}

func (i *Interceptor) BindRemoteStream(info *interceptor.StreamInfo, reader interceptor.RTPReader) interceptor.RTPReader {
	return interceptor.RTPReaderFunc(func() (*rtp.Packet, interceptor.Attributes, error) {
		// ensure chained buffer interceptor has been called
		p, att, err := reader.Read()

		stream := i.getStream(info.SSRC)
		if stream == nil {
			stream = i.newStream(info)
		}

		return p, att, err
	})
}

func (i *Interceptor) UnbindRemoteStream(info *interceptor.StreamInfo) {
	i.Lock()
	idx := -1
	for j, stream := range i.streams {
		if stream.Buffer.GetMediaSSRC() == info.SSRC {
			idx = j
			break
		}
	}
	if idx == -1 {
		i.Unlock()
		return
	}
	i.streams[idx] = i.streams[len(i.streams)-1]
	i.streams[len(i.streams)-1] = nil
	i.streams = i.streams[:len(i.streams)-1]
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
			case *rtcp.SourceDescription:
				for _, chunk := range pkt.Chunks {
					for _, s := range i.streams {
						if s.Buffer.GetMediaSSRC() == chunk.Source {
							for _, item := range chunk.Items {
								if item.Type == rtcp.SDESCNAME {
									s.setCName(item.Text)
								}
							}
						}
					}
				}
			case *rtcp.SenderReport:
				findRelatedCName := func(ssrc uint32) string {
					i.RLock()
					defer i.RUnlock()

					for _, s := range i.streams {
						if s.Buffer.GetMediaSSRC() == ssrc {
							return s.GetCName()
						}
					}
					return ""
				}

				calculateLatestMinMaxSenderNtpTime := func(cname string) (minPacketNtpTimeInMillisSinceSenderEpoch uint64, maxPacketNtpTimeInMillisSinceSenderEpoch uint64) {
					if len(cname) < 1 {
						return
					}
					i.RLock()
					defer i.RUnlock()

					for _, s := range i.streams {
						if s.GetCName() != cname {
							continue
						}

						clockRate := s.Buffer.GetClockRate()
						srrtp, srntp, _ := s.Buffer.GetSenderReportData()
						latestTimestamp, _ := s.Buffer.GetLatestTimestamp()

						fastForwardTimestampInClockRate := fastFowardTimestampAmount(latestTimestamp, srrtp)
						fastForwardTimestampInMillis := (fastForwardTimestampInClockRate * 1000) / clockRate
						latestPacketNtpTimeInMillisSinceSenderEpoch := ntpToMillisSinceEpoch(srntp) + uint64(fastForwardTimestampInMillis)

						if 0 == minPacketNtpTimeInMillisSinceSenderEpoch || latestPacketNtpTimeInMillisSinceSenderEpoch < minPacketNtpTimeInMillisSinceSenderEpoch {
							minPacketNtpTimeInMillisSinceSenderEpoch = latestPacketNtpTimeInMillisSinceSenderEpoch
						}
						if 0 == maxPacketNtpTimeInMillisSinceSenderEpoch || latestPacketNtpTimeInMillisSinceSenderEpoch > maxPacketNtpTimeInMillisSinceSenderEpoch {
							maxPacketNtpTimeInMillisSinceSenderEpoch = latestPacketNtpTimeInMillisSinceSenderEpoch
						}
					}
					return minPacketNtpTimeInMillisSinceSenderEpoch, maxPacketNtpTimeInMillisSinceSenderEpoch
				}

				setDrift := func(cname string, driftInMillis uint64) {
					if len(cname) < 1 {
						return
					}
					i.RLock()
					defer i.RUnlock()

					for _, s := range i.streams {
						if s.GetCName() != cname {
							continue
						}
						s.setDriftInMillis(driftInMillis)
					}
				}

				cname := findRelatedCName(pkt.SSRC)

				minPacketNtpTimeInMillisSinceSenderEpoch, maxPacketNtpTimeInMillisSinceSenderEpoch := calculateLatestMinMaxSenderNtpTime(cname)

				driftInMillis := maxPacketNtpTimeInMillisSinceSenderEpoch - minPacketNtpTimeInMillisSinceSenderEpoch

				setDrift(cname, driftInMillis)
			}
		}

		return pkts, attributes, nil
	})
}

func (i *Interceptor) BindRTCPWriter(writer interceptor.RTCPWriter) interceptor.RTCPWriter {
	return writer
}

func (i *Interceptor) getStream(ssrc uint32) *Stream {
	i.RLock()
	defer i.RUnlock()
	for _, b := range i.streams {
		if b.Buffer.GetMediaSSRC() == ssrc {
			return b
		}
	}
	return nil
}

func (i *Interceptor) newStream(info *interceptor.StreamInfo) *Stream {
	stream := NewStream(i.bufferInterceptor.GetBuffer(info.SSRC), info)
	i.Lock()
	i.streams = append(i.streams, stream)
	i.Unlock()
	return stream
}

func ntpToMillisSinceEpoch(ntp uint64) uint64 {
	// ntp time since epoch calculate fractional ntp as milliseconds
	// (lower 32 bits stored as 1/2^32 seconds) and add
	// ntp seconds (stored in higher 32 bits) as milliseconds
	return (((ntp & 0xFFFFFFFF) * 1000) >> 32) + ((ntp >> 32) * 1000)
}

func fastFowardTimestampAmount(newestTimestamp uint32, referenceTimestamp uint32) uint32 {
	if buffer.IsTimestampWrapAround(newestTimestamp, referenceTimestamp) {
		return uint32(uint64(newestTimestamp) + 0x100000000 - uint64(referenceTimestamp))
	}
	if newestTimestamp < referenceTimestamp {
		return 0
	}
	return newestTimestamp - referenceTimestamp
}
