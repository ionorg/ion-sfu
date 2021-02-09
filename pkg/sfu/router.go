package sfu

import (
	"sync"

	log "github.com/pion/ion-log"
	"github.com/pion/ion-sfu/pkg/buffer"
	"github.com/pion/ion-sfu/pkg/stats"
	"github.com/pion/ion-sfu/pkg/twcc"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

// Router defines a track rtp/rtcp router
type Router interface {
	ID() string
	AddReceiver(receiver *webrtc.RTPReceiver, track *webrtc.TrackRemote) (Receiver, bool)
	AddDownTracks(s *Subscriber, r Receiver) error
	Stop()
}

// RouterConfig defines router configurations
type RouterConfig struct {
	WithStats           bool            `mapstructure:"withstats"`
	MaxBandwidth        uint64          `mapstructure:"maxbandwidth"`
	MaxBufferTime       int             `mapstructure:"maxbuffertime"`
	AudioLevelInterval  int             `mapstructure:"audiolevelinterval"`
	AudioLevelThreshold uint8           `mapstructure:"audiolevelthreshold"`
	AudioLevelFilter    int             `mapstructure:"audiolevelfilter"`
	Simulcast           SimulcastConfig `mapstructure:"simulcast"`
}

type router struct {
	sync.RWMutex
	id        string
	twcc      *twcc.Responder
	peer      *webrtc.PeerConnection
	stats     map[uint32]*stats.Stream
	rtcpCh    chan []rtcp.Packet
	stopCh    chan struct{}
	config    RouterConfig
	session   *Session
	receivers map[string]Receiver
}

// newRouter for routing rtp/rtcp packets
func newRouter(peer *webrtc.PeerConnection, id string, session *Session, config RouterConfig) Router {
	ch := make(chan []rtcp.Packet, 10)
	r := &router{
		id:        id,
		peer:      peer,
		rtcpCh:    ch,
		stopCh:    make(chan struct{}),
		config:    config,
		session:   session,
		receivers: make(map[string]Receiver),
		stats:     make(map[uint32]*stats.Stream),
	}

	if config.WithStats {
		stats.Peers.Inc()
	}

	go r.sendRTCP()
	return r
}

func (r *router) ID() string {
	return r.id
}

func (r *router) Stop() {
	r.stopCh <- struct{}{}

	if r.config.WithStats {
		stats.Peers.Dec()
	}
}

func (r *router) AddReceiver(receiver *webrtc.RTPReceiver, track *webrtc.TrackRemote) (Receiver, bool) {
	r.Lock()
	defer r.Unlock()

	publish := false
	trackID := track.ID()
	rid := track.RID()

	buff, rtcpReader := bufferFactory.GetBufferPair(uint32(track.SSRC()))

	buff.OnFeedback(func(fb []rtcp.Packet) {
		r.rtcpCh <- fb
	})

	if track.Kind() == webrtc.RTPCodecTypeAudio {
		streamID := track.StreamID()
		buff.OnAudioLevel(func(level uint8) {
			r.session.audioObserver.observe(streamID, level)
		})
		r.session.audioObserver.addStream(streamID)

	} else if track.Kind() == webrtc.RTPCodecTypeVideo {
		if r.twcc == nil {
			r.twcc = twcc.NewTransportWideCCResponder(uint32(track.SSRC()))
			r.twcc.OnFeedback(func(p rtcp.RawPacket) {
				r.rtcpCh <- []rtcp.Packet{&p}
			})
		}
		buff.OnTransportWideCC(func(sn uint16, timeNS int64, marker bool) {
			r.twcc.Push(sn, timeNS, marker)
		})
	}

	if r.config.WithStats {
		r.stats[uint32(track.SSRC())] = stats.NewStream(buff)
	}

	rtcpReader.OnPacket(func(bytes []byte) {
		pkts, err := rtcp.Unmarshal(bytes)
		if err != nil {
			log.Errorf("Unmarshal rtcp receiver packets err: %v", err)
			return
		}
		for _, pkt := range pkts {
			switch pkt := pkt.(type) {
			case *rtcp.SourceDescription:
				if r.config.WithStats {
					for _, chunk := range pkt.Chunks {
						if s, ok := r.stats[chunk.Source]; ok {
							for _, item := range chunk.Items {
								if item.Type == rtcp.SDESCNAME {
									s.SetCName(item.Text)
								}
							}
						}
					}
				}
			case *rtcp.SenderReport:
				buff.SetSenderReportData(pkt.RTPTime, pkt.NTPTime)
				if r.config.WithStats {
					if st := r.stats[pkt.SSRC]; st != nil {
						r.updateStats(st)
					}
				}
			}
		}
	})

	recv, ok := r.receivers[trackID]
	if !ok {
		recv = NewWebRTCReceiver(receiver, track, r.id)
		r.receivers[trackID] = recv
		recv.SetRTCPCh(r.rtcpCh)
		recv.OnCloseHandler(func() {
			if r.config.WithStats {
				if track.Kind() == webrtc.RTPCodecTypeVideo {
					stats.VideoTracks.Dec()
				} else {
					stats.AudioTracks.Dec()
				}
			}
			if recv.Kind() == webrtc.RTPCodecTypeAudio {
				r.session.audioObserver.removeStream(track.StreamID())
			}
			r.deleteReceiver(trackID, uint32(track.SSRC()))
		})
		if len(rid) == 0 || r.config.Simulcast.BestQualityFirst && rid == fullResolution ||
			!r.config.Simulcast.BestQualityFirst && rid == quarterResolution {
			publish = true
		}
	} else if r.config.Simulcast.BestQualityFirst && rid == fullResolution ||
		!r.config.Simulcast.BestQualityFirst && rid == quarterResolution ||
		!r.config.Simulcast.BestQualityFirst && rid == halfResolution {
		publish = true
	}

	recv.AddUpTrack(track, buff)

	buff.Bind(receiver.GetParameters(), buffer.Options{
		BufferTime: r.config.MaxBufferTime,
		MaxBitRate: r.config.MaxBandwidth,
	})

	if r.config.WithStats {
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			stats.VideoTracks.Inc()
		} else {
			stats.AudioTracks.Inc()
		}
	}

	return recv, publish
}

// AddWebRTCSender to router
func (r *router) AddDownTracks(s *Subscriber, recv Receiver) error {
	r.Lock()
	defer r.Unlock()

	if recv != nil {
		if err := r.addDownTrack(s, recv); err != nil {
			return err
		}
		s.negotiate()
		return nil
	}

	if len(r.receivers) > 0 {
		for _, rcv := range r.receivers {
			if err := r.addDownTrack(s, rcv); err != nil {
				return err
			}
		}
		s.negotiate()
	}
	return nil
}

func (r *router) addDownTrack(sub *Subscriber, recv Receiver) error {
	for _, dt := range sub.GetDownTracks(recv.StreamID()) {
		if dt.ID() == recv.TrackID() {
			return nil
		}
	}

	codec := recv.Codec()
	if err := sub.me.RegisterCodec(codec, recv.Kind()); err != nil {
		return err
	}

	downTrack, err := NewDownTrack(webrtc.RTPCodecCapability{
		MimeType:     codec.MimeType,
		ClockRate:    codec.ClockRate,
		Channels:     codec.Channels,
		SDPFmtpLine:  codec.SDPFmtpLine,
		RTCPFeedback: []webrtc.RTCPFeedback{{"goog-remb", ""}, {"nack", ""}, {"nack", "pli"}},
	}, recv, sub.id)
	if err != nil {
		return err
	}
	// Create webrtc sender for the peer we are sending track to
	if downTrack.transceiver, err = sub.pc.AddTransceiverFromTrack(downTrack, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
	}); err != nil {
		return err
	}

	// nolint:scopelint
	downTrack.OnCloseHandler(func() {
		if sub.pc.ConnectionState() != webrtc.PeerConnectionStateClosed {
			if err := sub.pc.RemoveTrack(downTrack.transceiver.Sender()); err != nil {
				if err == webrtc.ErrConnectionClosed {
					return
				}
				log.Errorf("Error closing down track: %v", err)
			} else {
				sub.RemoveDownTrack(recv.StreamID(), downTrack)
				sub.negotiate()
			}
		}
	})

	downTrack.OnBind(func() {
		go sub.sendStreamDownTracksReports(recv.StreamID())
	})

	sub.AddDownTrack(recv.StreamID(), downTrack)
	recv.AddDownTrack(downTrack, r.config.Simulcast.BestQualityFirst)
	return nil
}

func (r *router) deleteReceiver(track string, ssrc uint32) {
	r.Lock()
	delete(r.receivers, track)
	delete(r.stats, ssrc)
	r.Unlock()
}

func (r *router) sendRTCP() {
	for {
		select {
		case pkts := <-r.rtcpCh:
			if err := r.peer.WriteRTCP(pkts); err != nil {
				log.Errorf("Write rtcp to peer %s err :%v", r.id, err)
			}
		case <-r.stopCh:
			return
		}
	}
}

func (r *router) updateStats(stream *stats.Stream) {
	calculateLatestMinMaxSenderNtpTime := func(cname string) (minPacketNtpTimeInMillisSinceSenderEpoch uint64, maxPacketNtpTimeInMillisSinceSenderEpoch uint64) {
		if len(cname) < 1 {
			return
		}
		r.RLock()
		defer r.RUnlock()

		for _, s := range r.stats {
			if s.GetCName() != cname {
				continue
			}

			clockRate := s.Buffer.GetClockRate()
			srrtp, srntp, _ := s.Buffer.GetSenderReportData()
			latestTimestamp, _ := s.Buffer.GetLatestTimestamp()

			fastForwardTimestampInClockRate := fastForwardTimestampAmount(latestTimestamp, srrtp)
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
		r.RLock()
		defer r.RUnlock()

		for _, s := range r.stats {
			if s.GetCName() != cname {
				continue
			}
			s.SetDriftInMillis(driftInMillis)
		}
	}

	cname := stream.GetCName()

	minPacketNtpTimeInMillisSinceSenderEpoch, maxPacketNtpTimeInMillisSinceSenderEpoch := calculateLatestMinMaxSenderNtpTime(cname)

	driftInMillis := maxPacketNtpTimeInMillisSinceSenderEpoch - minPacketNtpTimeInMillisSinceSenderEpoch

	setDrift(cname, driftInMillis)

	stream.CalcStats()
}
