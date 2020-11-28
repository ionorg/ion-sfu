package sfu

//go:generate go run github.com/matryer/moq -out router_mock_test.generated.go . Router

import (
	"io"
	"sync"
	"time"

	"github.com/pion/sdp/v3"

	"github.com/pion/webrtc/v3"

	log "github.com/pion/ion-log"
	"github.com/pion/rtcp"
)

const (
	SimpleReceiver = iota + 1
	SimulcastReceiver
	SVCReceiver
)

// Router defines a track rtp/rtcp router
type Router interface {
	ID() string
	Config() RouterConfig
	AddReceiver(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver, me *webrtc.MediaEngine) *receiverRouter
	AddDownTracks(s *Subscriber, rr *receiverRouter) error
	SendRTCP(pkts []rtcp.Packet)
	Stop()
}

// RouterConfig defines router configurations
type RouterConfig struct {
	MaxBandwidth  uint64          `mapstructure:"maxbandwidth"`
	MaxBufferTime int             `mapstructure:"maxbuffertime"`
	Simulcast     SimulcastConfig `mapstructure:"simulcast"`
}

type receiverRouter struct {
	kind      int
	stream    string
	receivers [3]Receiver
}

type router struct {
	id        string
	mu        sync.RWMutex
	peer      *webrtc.PeerConnection
	twcc      *TransportWideCC
	rtcpCh    chan []rtcp.Packet
	config    RouterConfig
	receivers map[string]*receiverRouter
}

// newRouter for routing rtp/rtcp packets
func newRouter(peer *webrtc.PeerConnection, id string, config RouterConfig) Router {
	ch := make(chan []rtcp.Packet, 10)
	r := &router{
		id:        id,
		peer:      peer,
		twcc:      newTransportWideCC(ch),
		config:    config,
		rtcpCh:    ch,
		receivers: make(map[string]*receiverRouter),
	}
	go r.sendRTCP()
	return r
}

func (r *router) ID() string {
	return r.id
}

func (r *router) Config() RouterConfig {
	return r.config
}

func (r *router) AddReceiver(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver, me *webrtc.MediaEngine) *receiverRouter {
	r.mu.Lock()
	defer r.mu.Unlock()

	trackID := track.ID()
	var twccExt uint8

	extID, audio, video := me.GetHeaderExtensionID(webrtc.RTPHeaderExtensionCapability{URI: sdp.TransportCCURI})
	if r.twcc.tccLastReport == 0 {
		r.twcc.tccLastReport = time.Now().UnixNano()
		r.twcc.mSSRC = uint32(track.SSRC())
	}
	if track.Kind() == webrtc.RTPCodecTypeVideo && video {
		twccExt = uint8(extID)
	}
	if track.Kind() == webrtc.RTPCodecTypeAudio && audio {
		twccExt = uint8(extID)
	}

	recv := NewWebRTCReceiver(receiver, track, BufferOptions{
		BufferTime: r.config.MaxBufferTime,
		MaxBitRate: r.config.MaxBandwidth * 1000,
		TWCCExt:    twccExt,
	})
	recv.OnTransportWideCC(func(sn uint16, timeNS int64, marker bool) {
		r.twcc.push(sn, timeNS, marker)
	})
	recv.SetRTCPCh(r.rtcpCh)
	recv.OnCloseHandler(func() {
		r.deleteReceiver(trackID)
	})
	recv.Start()

	if rr, ok := r.receivers[trackID]; ok {
		rr.receivers[recv.SpatialLayer()] = recv
		return nil
	}

	rr := &receiverRouter{
		stream:    track.StreamID(),
		receivers: [3]Receiver{},
	}
	rr.receivers[recv.SpatialLayer()] = recv

	if len(track.RID()) > 0 {
		rr.kind = SimulcastReceiver
	} else {
		rr.kind = SimpleReceiver
	}

	r.receivers[trackID] = rr
	return rr
}

// AddWebRTCSender to router
func (r *router) AddDownTracks(s *Subscriber, rr *receiverRouter) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if rr != nil {
		if err := r.addDownTrack(s, rr); err != nil {
			return err
		}
		s.negotiate()
		return nil
	}

	if len(r.receivers) > 0 {
		for _, rr = range r.receivers {
			if err := r.addDownTrack(s, rr); err != nil {
				return err
			}
		}
		s.negotiate()
	}
	return nil
}

func (r *router) SendRTCP(pkts []rtcp.Packet) {
	r.rtcpCh <- pkts
}

func (r *router) Stop() {
	close(r.rtcpCh)
}

func (r *router) addDownTrack(sub *Subscriber, rr *receiverRouter) error {
	var recv Receiver
	if rr.kind == SimpleReceiver {
		recv = rr.receivers[0]
	} else {
		for _, rcv := range rr.receivers {
			if rcv != nil {
				recv = rcv
			}
			if !r.config.Simulcast.BestQualityFirst && rcv != nil {
				break
			}
		}
	}

	if recv == nil {
		return errNoReceiverFound
	}

	for _, dt := range sub.GetDownTracks(rr.stream) {
		if dt.ID() == recv.Track().ID() {
			return nil
		}
	}

	inTrack := recv.Track()
	codec := inTrack.Codec()
	if err := sub.me.RegisterCodec(codec, inTrack.Kind()); err != nil {
		return err
	}

	outTrack, err := NewDownTrack(webrtc.RTPCodecCapability{
		MimeType:     codec.MimeType,
		ClockRate:    codec.ClockRate,
		Channels:     codec.Channels,
		SDPFmtpLine:  codec.SDPFmtpLine,
		RTCPFeedback: []webrtc.RTCPFeedback{{"goog-remb", ""}, {"ccm", "fir"}, {"nack", ""}, {"nack", "pli"}},
	}, rr, sub.id, inTrack.ID(), inTrack.StreamID())

	if err != nil {
		return err
	}
	// Create webrtc sender for the peer we are sending track to
	if outTrack.transceiver, err = sub.pc.AddTransceiverFromTrack(outTrack, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
	}); err != nil {
		return err
	}

	if rr.kind == SimulcastReceiver {
		outTrack.trackType = SimulcastDownTrack
		outTrack.currentSpatialLayer = recv.SpatialLayer()
	} else {
		outTrack.trackType = SimpleDownTrack
	}
	// nolint:scopelint
	outTrack.OnCloseHandler(func() {
		if err := sub.pc.RemoveTrack(outTrack.transceiver.Sender()); err != nil {
			log.Errorf("Error closing down track: %v", err)
		} else {
			sub.negotiate()
		}
	})
	go r.loopDownTrackRTCP(outTrack)
	sub.AddDownTrack(rr.stream, outTrack)
	recv.AddDownTrack(outTrack)
	return nil
}

func (r *router) loopDownTrackRTCP(track *DownTrack) {
	sender := track.transceiver.Sender()
	for {
		pkts, err := sender.ReadRTCP()
		if err == io.ErrClosedPipe || err == io.EOF {
			log.Debugf("Sender %s closed due to: %v", track.peerID, err)
			// Remove sender from receiver
			if recv := track.router.receivers[track.currentSpatialLayer]; recv != nil {
				recv.DeleteDownTrack(track.peerID)
			}
			track.Close()
			return
		}

		if err != nil {
			log.Errorf("rtcp err => %v", err)
			continue
		}

		var fwdPkts []rtcp.Packet
		pliOnce := true
		firOnce := true
		for _, pkt := range pkts {
			switch pkt := pkt.(type) {
			case *rtcp.PictureLossIndication:
				if track.enabled.get() && pliOnce {
					pkt.MediaSSRC = track.lastSSRC
					pkt.SenderSSRC = track.lastSSRC
					fwdPkts = append(fwdPkts, pkt)
					pliOnce = false
				}
			case *rtcp.FullIntraRequest:
				if track.enabled.get() && firOnce {
					pkt.MediaSSRC = track.lastSSRC
					pkt.SenderSSRC = track.ssrc
					fwdPkts = append(fwdPkts, pkt)
					firOnce = false
				}
			case *rtcp.ReceiverReport:
				if track.enabled.get() && len(pkt.Reports) > 0 && pkt.Reports[0].FractionLost > 25 {
					log.Tracef("Slow link for sender %s, fraction packet lost %.2f", track.id, float64(pkt.Reports[0].FractionLost)/256)
				}
			case *rtcp.TransportLayerNack:
				log.Tracef("sender got nack: %+v", pkt)
				recv := track.router.receivers[track.currentSpatialLayer]
				if recv == nil {
					continue
				}
				for _, pair := range pkt.Nacks {
					if err := recv.WriteBufferedPacket(track, track.nList.getNACKSeqNo(pair.PacketList())); err == errPacketNotFound {
						// TODO handle missing nacks in sfu cache
					}
				}
			}
		}
		if len(fwdPkts) > 0 {
			r.rtcpCh <- fwdPkts
		}
	}
}

func (r *router) deleteReceiver(track string) {
	r.mu.Lock()
	delete(r.receivers, track)
	r.mu.Unlock()
}

func (r *router) sendRTCP() {
	for pkts := range r.rtcpCh {
		if err := r.peer.WriteRTCP(pkts); err != nil {
			log.Errorf("Write rtcp to peer %s err :%v", r.id, err)
		}
	}
}
