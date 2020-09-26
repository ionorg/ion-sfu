package sfu

import (
	"math/rand"
	"sync"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
)

// RouterConfig defines router configurations
type RouterConfig struct {
	REMBFeedback bool                      `mapstructure:"subrembfeedback"`
	MaxBandwidth uint64                    `mapstructure:"maxbandwidth"`
	MaxNackTime  int64                     `mapstructure:"maxnacktime"`
	Video        WebRTCVideoReceiverConfig `mapstructure:"video"`
	Simulcast    SimulcastConfig           `mapstructure:"simulcast"`
}

// Router defines a track rtp/rtcp router
type Router struct {
	tid       string
	mu        sync.RWMutex
	config    RouterConfig
	receivers [3 + 1]Receiver
	lastNack  int64
	simulcast bool
}

// NewRouter for routing rtp/rtcp packets
func NewRouter(tid string, config RouterConfig, hasSimulcast bool) *Router {
	r := &Router{
		tid:       tid,
		config:    config,
		lastNack:  time.Now().Unix(),
		simulcast: hasSimulcast,
	}
	return r
}

func (r *Router) AddReceiver(recv Receiver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.receivers[recv.SpatialLayer()] = recv
}

func (r *Router) GetReceiver(layer uint8) Receiver {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.receivers[layer]
}

// AddWebRTCSender to router
func (r *Router) AddWebRTCSender(p *WebRTCTransport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var (
		recv   Receiver
		sender Sender
		ssrc   uint32
	)

	if !r.simulcast {
		recv = r.receivers[0]
		ssrc = recv.Track().SSRC()
	} else {
		for _, rcv := range r.receivers {
			recv = rcv
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
	// TODO: Fix label issue in simulcast
	label := inTrack.Label()
	if len(label) < 5 {
		label = "fixme"
	}
	outTrack, err := p.pc.NewTrack(pt, ssrc, inTrack.ID(), label)
	if err != nil {
		return err
	}
	// Create webrtc sender for the peer we are sending track to
	s, err := p.pc.AddTrack(outTrack)
	if err != nil {
		return err
	}
	if r.simulcast {
		sender = NewWebRTCSimulcastSender(p.ctx, p.id, r, s, recv.SpatialLayer())
	} else {
		sender = NewWebRTCSender(p.ctx, p.id, r, s)
	}
	sender.OnCloseHandler(func() {
		if err := p.pc.RemoveTrack(s); err != nil {
			log.Errorf("Error closing sender: %s", err)
		}
	})
	recv.AddSender(sender)
	return nil
}

func (r *Router) SwitchSpatialLayer(currentLayer, targetLayer uint8, sub Sender) bool {
	currentRecv := r.GetReceiver(currentLayer)
	targetRecv := r.GetReceiver(targetLayer)
	if targetRecv != nil {
		// TODO do a more smart layer change
		currentRecv.DeleteSender(sub.ID())
		targetRecv.AddSender(sub)
		return true
	}
	return false
}

func (r *Router) stats() string {
	return ""
}
