package sfu

//go:generate go run github.com/matryer/moq -out router_mock_test.generated.go . Router

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/pion/webrtc/v3"

	"github.com/pion/ion-sfu/pkg/log"
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
	AddReceiver(ctx context.Context, track *webrtc.Track, receiver *webrtc.RTPReceiver)
	AddSender(p *WebRTCTransport) error
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

func (r *router) AddReceiver(ctx context.Context, track *webrtc.Track, receiver *webrtc.RTPReceiver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	recv := NewWebRTCReceiver(ctx, receiver, track, r.config)
	recv.OnTransportCC(func(sn uint16, timeNS int64, marker bool) {
		r.twcc.push(sn, timeNS, marker)
	})
	recv.SetRTCPCh(r.rtcpCh)
	recv.OnCloseHandler(func() {
		r.deleteReceiver(recv.Track().ID())
	})
	if track.Kind() == webrtc.RTPCodecTypeVideo {
		r.twcc.mSSRC = track.SSRC()
		r.twcc.tccLastReport = time.Now().UnixNano()
	}
	recv.Start()

	if rr, ok := r.receivers[track.ID()]; ok {
		rr.receivers[recv.SpatialLayer()] = recv
		return
	}

	rr := &receiverRouter{
		stream:    track.Label(),
		receivers: [3]Receiver{},
	}
	rr.receivers[recv.SpatialLayer()] = recv

	if len(track.RID()) > 0 {
		rr.kind = SimulcastReceiver
	} else {
		rr.kind = SimpleReceiver
	}

	r.receivers[track.ID()] = rr
}

// AddWebRTCSender to router
func (r *router) AddSender(p *WebRTCTransport) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, rr := range r.receivers {
		var (
			recv   Receiver
			sender Sender
			ssrc   uint32
		)

		if rr.kind == SimpleReceiver {
			recv = rr.receivers[0]
			ssrc = recv.Track().SSRC()
		} else {
			for _, rcv := range rr.receivers {
				if rcv != nil {
					recv = rcv
				}
				if !r.config.Simulcast.BestQualityFirst && rcv != nil {
					break
				}
			}
			ssrc = rand.Uint32()
		}

		if recv == nil {
			return errNoReceiverFound
		}

		inTrack := recv.Track()
		to := p.me.GetCodecsByName(recv.Track().Codec().Name)
		if len(to) == 0 {
			return errPtNotSupported
		}
		pt := to[0].PayloadType
		outTrack, err := p.pc.NewTrack(pt, ssrc, inTrack.ID(), inTrack.Label())
		if err != nil {
			return err
		}
		// Create webrtc sender for the peer we are sending track to
		s, err := p.pc.AddTrack(outTrack)
		if err != nil {
			return err
		}
		if rr.kind == SimulcastReceiver {
			sender = NewSimulcastSender(p.ctx, p.id, rr, s, recv.SpatialLayer(), r.config.Simulcast)
		} else {
			sender = NewSimpleSender(p.ctx, p.id, rr, s)
		}
		sender.OnCloseHandler(func() {
			if err := p.pc.RemoveTrack(s); err != nil {
				log.Errorf("Error closing sender: %s", err)
			}
		})
		p.AddSender(rr.stream, sender)
		go func() {
			// There exists a bug in chrome where setLocalDescription
			// fails if track RTP arrives before the sfu offer is set.
			// We delay sending RTP here to avoid the issue.
			// https://bugs.chromium.org/p/webrtc/issues/detail?id=10139
			time.Sleep(500 * time.Millisecond)
			recv.AddSender(sender)
		}()
	}
	return nil
}

func (r *router) SendRTCP(pkts []rtcp.Packet) {
	r.rtcpCh <- pkts
}

func (r *router) Stop() {
	close(r.rtcpCh)
}

func (r *router) deleteReceiver(track string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.receivers, track)
}

func (r *router) sendRTCP() {
	for pkts := range r.rtcpCh {
		if err := r.peer.WriteRTCP(pkts); err != nil {
			log.Errorf("Write rtcp to peer %s err :%v", r.id, err)
		}
	}
}
