package stats

import (
	"math"
	"sync"

	"github.com/pion/interceptor"
	"github.com/pion/ion-sfu/pkg/buffer"
	"github.com/pion/rtcp"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	driftBuckets = []float64{5, 10, 20, 40, 80, 160, math.Inf(+1)}

	drift = prometheus.NewHistogram(prometheus.HistogramOpts{
		Subsystem: "rtp",
		Name:      "drift_millis",
		Buckets:   driftBuckets,
	})

	expectedCount = prometheus.NewCounter(prometheus.CounterOpts{
		Subsystem: "rtp",
		Name:      "expected",
	})

	receivedCount = prometheus.NewCounter(prometheus.CounterOpts{
		Subsystem: "rtp",
		Name:      "received",
	})

	packetCount = prometheus.NewCounter(prometheus.CounterOpts{
		Subsystem: "rtp",
		Name:      "packets",
	})

	totalBytes = prometheus.NewCounter(prometheus.CounterOpts{
		Subsystem: "rtp",
		Name:      "bytes",
	})

	expectedMinusReceived = prometheus.NewSummary(prometheus.SummaryOpts{
		Subsystem: "rtp",
		Name:      "expected_minus_received",
	})

	lostRate = prometheus.NewSummary(prometheus.SummaryOpts{
		Subsystem: "rtp",
		Name:      "lost_rate",
	})

	jitter = prometheus.NewSummary(prometheus.SummaryOpts{
		Subsystem: "rtp",
		Name:      "jitter",
	})
)

func init() {
	prometheus.MustRegister(drift)
	prometheus.MustRegister(expectedCount)
	prometheus.MustRegister(receivedCount)
	prometheus.MustRegister(packetCount)
	prometheus.MustRegister(totalBytes)
	prometheus.MustRegister(expectedMinusReceived)
	prometheus.MustRegister(lostRate)
	prometheus.MustRegister(jitter)
}

type Interceptor struct {
	sync.RWMutex
	bufferFactory *buffer.Factory
	streams       map[uint32]*Stream
	interceptor.NoOp
}

func NewStreamInterceptor(f *buffer.Factory) *Interceptor {
	return &Interceptor{
		bufferFactory: f,
		streams:       make(map[uint32]*Stream),
	}
}

func (i *Interceptor) BindRemoteStream(info *interceptor.StreamInfo, reader interceptor.RTPReader) interceptor.RTPReader {
	return interceptor.RTPReaderFunc(func(b []byte, a interceptor.Attributes) (int, interceptor.Attributes, error) {
		// ensure chained buffer interceptor has been called
		n, att, err := reader.Read(b, a)

		i.Lock()
		if i.streams[info.SSRC] == nil {
			i.streams[info.SSRC] = NewStream(i.bufferFactory.GetBuffer(info.SSRC), info)
		}
		i.Unlock()

		return n, att, err
	})
}

func (i *Interceptor) UnbindRemoteStream(info *interceptor.StreamInfo) {
	i.Lock()
	if i.streams[info.SSRC] != nil {
		delete(i.streams, info.SSRC)
	}
	i.Unlock()
}

func (i *Interceptor) BindRTCPReader(reader interceptor.RTCPReader) interceptor.RTCPReader {
	return interceptor.RTCPReaderFunc(func(b []byte, a interceptor.Attributes) (int, interceptor.Attributes, error) {
		n, attributes, err := reader.Read(b, a)
		if err != nil {
			return 0, nil, err
		}

		pkts, err := rtcp.Unmarshal(b[:n])
		if err != nil {
			return 0, nil, err
		}

		for _, pkt := range pkts {
			switch pkt := pkt.(type) {
			case *rtcp.SourceDescription:
				for _, chunk := range pkt.Chunks {
					if s := i.streams[chunk.Source]; s != nil {
						for _, item := range chunk.Items {
							if item.Type == rtcp.SDESCNAME {
								s.setCName(item.Text)
							}
						}
					}
				}
			case *rtcp.ReceiverReport:
				calculateStats := func(ssrc uint32) {
					i.RLock()
					defer i.RUnlock()

					if s := i.streams[ssrc]; s != nil {
						bufferStats := s.Buffer.GetStats()

						hadStats, diffStats := s.updateStats(bufferStats)

						if hadStats {
							expectedCount.Add(float64(diffStats.LastExpected))
							receivedCount.Add(float64(diffStats.LastReceived))
							packetCount.Add(float64(diffStats.PacketCount))
							totalBytes.Add(float64(diffStats.TotalByte))
						}

						expectedMinusReceived.Observe(float64(bufferStats.LastExpected - bufferStats.LastReceived))
						lostRate.Observe(float64(bufferStats.LostRate))
						jitter.Observe(float64(bufferStats.Jitter))
					}
				}
				calculateStats(pkt.SSRC)

			case *rtcp.SenderReport:
				findRelatedCName := func(ssrc uint32) string {
					i.RLock()
					defer i.RUnlock()

					if s := i.streams[ssrc]; s != nil {
						return s.GetCName()
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

				calculateStats := func(ssrc uint32) {
					i.RLock()
					defer i.RUnlock()

					for _, s := range i.streams {
						if s.Buffer.GetMediaSSRC() != ssrc {
							continue
						}

						bufferStats := s.Buffer.GetStats()
						driftInMillis := s.getDriftInMillis()

						hadStats, diffStats := s.updateStats(bufferStats)

						drift.Observe(float64(driftInMillis))
						if hadStats {
							expectedCount.Add(float64(diffStats.LastExpected))
							receivedCount.Add(float64(diffStats.LastReceived))
							packetCount.Add(float64(diffStats.PacketCount))
							totalBytes.Add(float64(diffStats.TotalByte))
						}

						expectedMinusReceived.Observe(float64(bufferStats.LastExpected - bufferStats.LastReceived))
						lostRate.Observe(float64(bufferStats.LostRate))
						jitter.Observe(float64(bufferStats.Jitter))
					}
				}

				cname := findRelatedCName(pkt.SSRC)

				minPacketNtpTimeInMillisSinceSenderEpoch, maxPacketNtpTimeInMillisSinceSenderEpoch := calculateLatestMinMaxSenderNtpTime(cname)

				driftInMillis := maxPacketNtpTimeInMillisSinceSenderEpoch - minPacketNtpTimeInMillisSinceSenderEpoch

				setDrift(cname, driftInMillis)
				calculateStats(pkt.SSRC)
			}
		}

		return n, attributes, nil
	})
}

func (i *Interceptor) BindRTCPWriter(writer interceptor.RTCPWriter) interceptor.RTCPWriter {
	return writer
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
