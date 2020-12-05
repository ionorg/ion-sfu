package sfu

//go:generate go run github.com/matryer/moq -out router_mock_test.generated.go . Router

import (
	"io"
	"sync"
	"time"

	"github.com/gammazero/workerpool"
	log "github.com/pion/ion-log"
	"github.com/pion/rtcp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
)

const (
	maxNackWorkers = 2
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
	MaxBandwidth  uint64          `mapstructure:"maxbandwidth"`
	MaxBufferTime int             `mapstructure:"maxbuffertime"`
	Simulcast     SimulcastConfig `mapstructure:"simulcast"`
}

type router struct {
	id        string
	mu        sync.RWMutex
	wp        *workerpool.WorkerPool
	peer      *webrtc.PeerConnection
	twcc      *TransportWideCC
	rtcpCh    chan []rtcp.Packet
	config    RouterConfig
	receivers map[string]Receiver
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
		receivers: make(map[string]Receiver),
		wp:        workerpool.New(maxNackWorkers),
	}
	go r.sendRTCP()
	return r
}

func (r *router) ID() string {
	return r.id
}

func (r *router) AddReceiver(receiver *webrtc.RTPReceiver, track *webrtc.TrackRemote) (Receiver, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	publish := false
	trackID := track.ID()

	recv := r.receivers[trackID]
	if recv == nil {
		recv = NewWebRTCReceiver(receiver, track, r.id)
		r.receivers[trackID] = recv
		publish = true
	}
	var twccExt uint8

	for _, ext := range receiver.GetParameters().HeaderExtensions {
		if ext.URI == sdp.TransportCCURI {
			twccExt = uint8(ext.ID)
			break
		}
	}
	if r.twcc.tccLastReport == 0 && twccExt != 0 {
		r.twcc.tccLastReport = time.Now().UnixNano()
		r.twcc.mSSRC = uint32(track.SSRC())
	}

	layer := recv.AddUpTrack(track, BufferOptions{
		BufferTime: r.config.MaxBufferTime,
		MaxBitRate: r.config.MaxBandwidth * 1000,
		TWCCExt:    twccExt,
	})
	recv.OnTransportWideCC(layer, func(sn uint16, timeNS int64, marker bool) {
		r.twcc.push(sn, timeNS, marker)
	})
	recv.SetRTCPCh(r.rtcpCh)
	recv.OnCloseHandler(func() {
		r.deleteReceiver(trackID)
	})
	recv.Start(layer)

	return recv, publish
}

// AddWebRTCSender to router
func (r *router) AddDownTracks(s *Subscriber, recv Receiver) error {
	r.mu.Lock()
	defer r.mu.Unlock()

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

func (r *router) Stop() {
	close(r.rtcpCh)
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

	outTrack, err := NewDownTrack(webrtc.RTPCodecCapability{
		MimeType:     codec.MimeType,
		ClockRate:    codec.ClockRate,
		Channels:     codec.Channels,
		SDPFmtpLine:  codec.SDPFmtpLine,
		RTCPFeedback: []webrtc.RTCPFeedback{{"goog-remb", ""}, {"nack", ""}, {"nack", "pli"}},
	}, recv, sub.id, recv.TrackID(), recv.StreamID())
	if err != nil {
		return err
	}
	// Create webrtc sender for the peer we are sending track to
	if outTrack.transceiver, err = sub.pc.AddTransceiverFromTrack(outTrack, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
	}); err != nil {
		return err
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
	sub.AddDownTrack(recv.StreamID(), outTrack)
	recv.AddDownTrack(outTrack, r.config.Simulcast.BestQualityFirst)
	return nil
}

func (r *router) loopDownTrackRTCP(track *DownTrack) {
	sender := track.transceiver.Sender()
	for {
		pkts, err := sender.ReadRTCP()
		if err == io.ErrClosedPipe || err == io.EOF {
			log.Debugf("Sender %s closed due to: %v", track.peerID, err)
			// Remove sender from receiver
			track.receiver.DeleteDownTrack(track.currentSpatialLayer, track.peerID)
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
				for _, pair := range pkt.Nacks {
					r.wp.Submit(func() {
						pkts := track.receiver.GetBufferedPackets(track.currentSpatialLayer, track.snOffset, track.tsOffset, track.nList.getNACKSeqNo(pair.PacketList()))
						for _, pkt := range pkts {
							_ = track.WriteRTP(&pkt)
						}
					})
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
