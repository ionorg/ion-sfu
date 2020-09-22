package sfu

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
)

var routerConfig RouterConfig

// Router defines a track rtp/rtcp router
type Router struct {
	tid       string
	mu        sync.RWMutex
	receivers []Receiver
	lastNack  int64

	simulcast     bool
	simulcastSSRC uint32
}

// NewRouter for routing rtp/rtcp packets
func NewRouter(tid string) *Router {
	r := &Router{
		tid:      tid,
		lastNack: time.Now().Unix(),
	}
	return r
}

// NewRouterWithSimulcast
func NewRouterWithSimulcast(tid string) *Router {
	r := &Router{
		tid:           tid,
		lastNack:      time.Now().Unix(),
		simulcast:     true,
		simulcastSSRC: rand.Uint32(),
	}
	return r
}

func (r *Router) AddReceiver(recv Receiver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.simulcast {
		recv.SetSimulcast(r.simulcastSSRC)
	}
	r.receivers = append(r.receivers, recv)
}

// AddSender to router
func (r *Router) AddSender(pid string, sub Sender) {
	r.receivers[0].AddSender(pid, sub)
	go r.subFeedbackLoop(pid, r.receivers[0], sub)
}

// subFeedbackLoop reads rtcp packets from the sub
// and either handles them or forwards them to the receivers.
func (r *Router) subFeedbackLoop(pid string, recv Receiver, sub Sender) {
	defer func() {
		recv.DeleteSender(pid)
	}()

	for {
		select {
		case pkt, opn := <-sub.ReadRTCP():
			if !opn {
				return
			}
			switch pkt := pkt.(type) {
			case *rtcp.TransportLayerNack:
				log.Tracef("Router got nack: %+v", pkt)
				for _, pair := range pkt.Nacks {
					bufferPkt := recv.GetPacket(pair.PacketID)
					if bufferPkt != nil {
						// We found the packet in the buffer, resend to sub
						sub.WriteRTP(bufferPkt)
						continue
					}
					if routerConfig.MaxNackTime > 0 {
						ln := atomic.LoadInt64(&r.lastNack)
						if (time.Now().Unix() - ln) < routerConfig.MaxNackTime {
							continue
						}
						atomic.StoreInt64(&r.lastNack, time.Now().Unix())
					}
					// Packet not found, request from receivers
					nack := &rtcp.TransportLayerNack{
						// origin ssrc
						SenderSSRC: pkt.SenderSSRC,
						MediaSSRC:  pkt.MediaSSRC,
						Nacks:      []rtcp.NackPair{{PacketID: pair.PacketID}},
					}
					if err := recv.WriteRTCP(nack); err != nil {
						log.Errorf("Error writing nack RTCP %s", err)
					}
				}
			default:
				if err := recv.WriteRTCP(pkt); err != nil {
					log.Errorf("Error writing RTCP %s", err)
				}
			}
		case layer := <-sub.Switch():
			if layer == recv.SpatialLayer() {
				// Same layer ignore msg
				continue
			}
			if rcv := r.switchSpatialLayer(layer); rcv != nil {
				recv.DeleteSender(pid)
				rcv.AddSender(pid, sub)
				go r.subFeedbackLoop(pid, rcv, sub)
				return
			}
		}
	}
}

func (r *Router) switchSpatialLayer(layer uint8) Receiver {
	for _, recv := range r.receivers {
		if recv.SpatialLayer() == layer {
			return recv
		}
	}
	return nil
}

func (r *Router) stats() string {
	return ""
}
