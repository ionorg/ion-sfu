package sfu

import (
	"sync"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/util"
	"github.com/pion/rtcp"
)

// Router defines a track rtp/rtcp router
type Router struct {
	stop     bool
	pub      Receiver
	subs     map[string]*Sender
	subsLock sync.RWMutex
}

// NewRouter for routing rtp/rtcp packets
func NewRouter(recv Receiver) *Router {
	r := &Router{
		pub:  recv,
		subs: make(map[string]*Sender),
	}

	go r.start()

	return r
}

// AddSub to router
func (r *Router) AddSub(pid string, sub *Sender) {
	r.subsLock.Lock()
	r.subs[pid] = sub
	r.subsLock.Unlock()

	go r.subFeedbackLoop(sub)
}

// Close a router
func (r *Router) Close() {
	r.stop = true

	// Close subs
	r.subsLock.Lock()
	for pid, sub := range r.subs {
		sub.Close()
		delete(r.subs, pid)
	}
	r.subsLock.Unlock()
	r.pub.Close()
}

func (r *Router) start() {
	defer util.Recover("[Router.start]")
	for {
		if r.stop {
			return
		}

		// get rtp from pub
		pkt, err := r.pub.ReadRTP()
		if err != nil {
			log.Errorf("r.pub.ReadRTP err=%v", err)
			continue
		}

		if pkt == nil {
			continue
		}

		r.subsLock.RLock()
		// Push to client send queues
		for _, sub := range r.subs {
			// TODO: Nonblock sending?
			sub.WriteRTP(pkt)
		}
		r.subsLock.RUnlock()
	}
}

// subFeedbackLoop reads rtcp packets from the sub
// and either handles them or forwards them to the pub.
func (r *Router) subFeedbackLoop(sub *Sender) {
	for {
		if r.stop {
			return
		}

		pkt, err := sub.ReadRTCP()

		if err != nil {
			continue
		}

		switch pkt := pkt.(type) {
		case *rtcp.TransportLayerNack:
			log.Infof("Router got nack: %+v", pkt)
			for _, pair := range pkt.Nacks {
				bufferpkt := r.pub.GetPacket(pair.PacketID)
				if bufferpkt != nil {
					// We found the packet in the buffer, resend to sub
					sub.WriteRTP(bufferpkt)
					continue
				}

				// Packet not found, request from pub
				nack := &rtcp.TransportLayerNack{
					//origin ssrc
					SenderSSRC: pkt.SenderSSRC,
					MediaSSRC:  pkt.MediaSSRC,
					Nacks:      []rtcp.NackPair{{PacketID: pair.PacketID}},
				}
				r.pub.WriteRTCP(nack)
			}
		default:
			r.pub.WriteRTCP(pkt)
		}
	}
}
