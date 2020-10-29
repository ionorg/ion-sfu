package sfu

//go:generate go run github.com/matryer/moq -out router_mock_test.generated.go . Router

import (
	"math/rand"
	"sync"
	"time"

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
	AddReceiver(track *webrtc.Track, receiver *webrtc.RTPReceiver) *receiverRouter
	AddSender(p *WebRTCTransport, rr *receiverRouter) error
	AddTWCCExt(id string, ext int)
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
	twccExts  map[string]int
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
		twccExts:  make(map[string]int),
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

func (r *router) AddReceiver(track *webrtc.Track, receiver *webrtc.RTPReceiver) *receiverRouter {
	r.mu.Lock()
	defer r.mu.Unlock()

	trackID := track.ID()
	recv := NewWebRTCReceiver(receiver, track, BufferOptions{
		BufferTime: r.config.MaxBufferTime,
		MaxBitRate: r.config.MaxBandwidth * 1000,
		TWCCExt:    r.twccExts[trackID],
	})
	recv.OnTransportWideCC(func(sn uint16, timeNS int64, marker bool) {
		r.twcc.push(sn, timeNS, marker)
	})
	recv.SetRTCPCh(r.rtcpCh)
	recv.OnCloseHandler(func() {
		r.deleteReceiver(trackID)
	})
	if track.Kind() == webrtc.RTPCodecTypeVideo {
		r.twcc.mSSRC = track.SSRC()
		r.twcc.tccLastReport = time.Now().UnixNano()
	}
	recv.Start()

	if rr, ok := r.receivers[trackID]; ok {
		rr.receivers[recv.SpatialLayer()] = recv
		return nil
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

	r.receivers[trackID] = rr
	return rr
}

// AddWebRTCSender to router
func (r *router) AddSender(p *WebRTCTransport, rr *receiverRouter) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if rr != nil {
		return r.addSender(p, rr)
	}

	for _, rr = range r.receivers {
		if err := r.addSender(p, rr); err != nil {
			return err
		}
	}
	return nil
}

func (r *router) AddTWCCExt(id string, ext int) {
	r.twccExts[id] = ext
}

func (r *router) SendRTCP(pkts []rtcp.Packet) {
	r.rtcpCh <- pkts
}

func (r *router) Stop() {
	close(r.rtcpCh)
}

func (r *router) addSender(p *WebRTCTransport, rr *receiverRouter) error {
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
	t, err := p.pc.AddTransceiverFromTrack(outTrack, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
	})
	if err != nil {
		return err
	}
	if rr.kind == SimulcastReceiver {
		sender = NewSimulcastSender(p.id, rr, t.Sender(), recv.SpatialLayer(), r.config.Simulcast)
	} else {
		sender = NewSimpleSender(p.id, rr, t.Sender())
	}
	sender.OnCloseHandler(func() {
		if err := p.pc.RemoveTrack(t.Sender()); err != nil {
			log.Errorf("Error closing sender: %s", err)
		}
	})
	p.pendingSenders.PushBack(&pendingSender{
		transceiver: t,
		sender:      sender,
	})
	p.AddSender(rr.stream, sender)
	recv.AddSender(sender)
	return nil
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
