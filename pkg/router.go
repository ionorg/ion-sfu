package sfu

//go:generate go run github.com/matryer/moq -out router_mock_test.generated.go . Router

import (
	"math/rand"
	"sync"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
)

const (
	SimpleRouter = iota + 1
	SimulcastRouter
	SVCRouter
)

// Router defines a track rtp/rtcp router
type Router interface {
	ID() string
	Kind() int
	Config() RouterConfig
	AddReceiver(recv Receiver)
	GetReceiver(layer uint8) Receiver
	AddSender(p *WebRTCTransport) error
	GetRTCP() []rtcp.Packet
	SendRTCP(pkts []rtcp.Packet) error
	SwitchSpatialLayer(targetLayer uint8, sub Sender) bool
}

// RouterConfig defines router configurations
type RouterConfig struct {
	MaxBandwidth  uint64          `mapstructure:"maxbandwidth"`
	MaxBufferTime int             `mapstructure:"maxbuffertime"`
	Simulcast     SimulcastConfig `mapstructure:"simulcast"`
}

type router struct {
	mu        sync.RWMutex
	peer      *WebRTCTransport
	kind      int
	config    RouterConfig
	streamID  string
	receivers [3 + 1]Receiver
}

// newRouter for routing rtp/rtcp packets
func newRouter(peer *WebRTCTransport, streamID string, config RouterConfig, kind int) Router {
	return &router{
		peer:     peer,
		kind:     kind,
		config:   config,
		streamID: streamID,
	}
}

func (r *router) ID() string {
	return r.peer.id
}

func (r *router) Kind() int {
	return r.kind
}

func (r *router) Config() RouterConfig {
	return r.config
}

func (r *router) AddReceiver(recv Receiver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.receivers[recv.SpatialLayer()] = recv
}

func (r *router) GetReceiver(layer uint8) Receiver {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.receivers[layer]
}

// AddWebRTCSender to router
func (r *router) AddSender(p *WebRTCTransport) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var (
		recv   Receiver
		sender Sender
		ssrc   uint32
	)

	if r.kind == SimpleRouter {
		recv = r.receivers[0]
		ssrc = recv.Track().SSRC()
	} else {
		for _, rcv := range r.receivers {
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
	if r.kind == SimulcastRouter {
		sender = NewSimulcastSender(p.ctx, p.id, r, s, recv.SpatialLayer())
	} else {
		sender = NewSimpleSender(p.ctx, p.id, r, s)
	}
	sender.OnCloseHandler(func() {
		if err := p.pc.RemoveTrack(s); err != nil {
			log.Errorf("Error closing sender: %s", err)
		}
	})
	p.AddSender(r.streamID, sender)
	go func() {
		// There exists a bug in chrome where setLocalDescription
		// fails if track RTP arrives before the sfu offer is set.
		// We delay sending RTP here to avoid the issue.
		// https://bugs.chromium.org/p/webrtc/issues/detail?id=10139
		time.Sleep(500 * time.Millisecond)
		recv.AddSender(sender)
	}()
	return nil
}

func (r *router) SendRTCP(pkts []rtcp.Packet) error {
	return r.peer.pc.WriteRTCP(pkts)
}

func (r *router) GetRTCP() []rtcp.Packet {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.kind == SimpleRouter || r.kind == SVCRouter {
		if r.receivers[0] != nil {
			rr, ps := r.receivers[0].GetRTCP()
			if rr.SSRC != 0 {
				ps = append(ps, &rtcp.ReceiverReport{
					Reports: []rtcp.ReceptionReport{rr},
				})
			}
			return ps
		}
		return nil
	}
	var rtcpPkts []rtcp.Packet
	var rReports []rtcp.ReceptionReport
	for _, recv := range r.receivers {
		if recv != nil {
			rr, ps := recv.GetRTCP()
			rtcpPkts = append(rtcpPkts, ps...)
			if rr.SSRC != 0 {
				rReports = append(rReports, rr)
			}
		}
	}
	if len(rReports) > 0 {
		rtcpPkts = append(rtcpPkts, &rtcp.ReceiverReport{
			Reports: rReports,
		})
	}
	return rtcpPkts
}

func (r *router) SwitchSpatialLayer(targetLayer uint8, sub Sender) bool {
	if targetRecv := r.GetReceiver(targetLayer); targetRecv != nil {
		targetRecv.AddSender(sub)
		return true
	}
	return false
}
