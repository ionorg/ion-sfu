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
	stopLock sync.RWMutex
	pub      Receiver
	pubLock  sync.RWMutex
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

// DelSub to router
func (r *Router) DelSub(pid string) {
	r.subsLock.Lock()
	delete(r.subs, pid)
	r.subsLock.Unlock()
}

// Close a router
func (r *Router) Close() {
	r.stopLock.Lock()
	r.stop = true
	r.stopLock.Unlock()

	// Close subs
	r.subsLock.Lock()
	for pid, sub := range r.subs {
		sub.Close()
		delete(r.subs, pid)
	}
	r.subsLock.Unlock()
	r.pubLock.Lock()
	r.pub.Close()
	r.pubLock.Unlock()
}

func (r *Router) start() {
	defer util.Recover("[Router.start]")
	for {
		r.stopLock.RLock()
		if r.stop {
			r.stopLock.RUnlock()
			return
		}
		r.stopLock.RUnlock()

		// get rtp from pub
		r.pubLock.RLock()
		pkt, err := r.pub.ReadRTP()
		r.pubLock.RUnlock()
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
			err := sub.WriteRTP(pkt)
			if err != nil {
				log.Errorf("Error writing RTP %s", err)
			}
		}
		r.subsLock.RUnlock()
	}
}

// subFeedbackLoop reads rtcp packets from the sub
// and either handles them or forwards them to the pub.
func (r *Router) subFeedbackLoop(sub *Sender) {
	for {
		r.stopLock.RLock()
		if r.stop {
			r.stopLock.RUnlock()
			return
		}
		r.stopLock.RUnlock()

		pkt, err := sub.ReadRTCP()

		if err != nil {
			log.Errorf("sub nil rtcp packet")
			return
		}

		switch pkt := pkt.(type) {
		case *rtcp.TransportLayerNack:
			log.Infof("Router got nack: %+v", pkt)
			for _, pair := range pkt.Nacks {
				r.pubLock.RLock()
				bufferpkt := r.pub.GetPacket(pair.PacketID)
				r.pubLock.RUnlock()
				if bufferpkt != nil {
					// We found the packet in the buffer, resend to sub
					err = sub.WriteRTP(bufferpkt)
					if err != nil {
						log.Errorf("error writing rtp %s", err)
					}
					continue
				}

				// Packet not found, request from pub
				nack := &rtcp.TransportLayerNack{
					//origin ssrc
					SenderSSRC: pkt.SenderSSRC,
					MediaSSRC:  pkt.MediaSSRC,
					Nacks:      []rtcp.NackPair{{PacketID: pair.PacketID}},
				}
				r.pubLock.RLock()
				err = r.pub.WriteRTCP(nack)
				r.pubLock.RUnlock()
				if err != nil {
					log.Errorf("Error writing nack RTCP %s", err)
				}
			}
		default:
			r.pubLock.RLock()
			err = r.pub.WriteRTCP(pkt)
			r.pubLock.RUnlock()
			if err != nil {
				log.Errorf("Error writing RTCP %s", err)
			}
		}
	}
}
