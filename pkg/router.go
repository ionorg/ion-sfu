package sfu

import (
	"fmt"
	"sync"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/util"
	"github.com/pion/rtcp"
)

// Router defines a track rtp/rtcp router
type Router struct {
	tid      string
	stop     bool
	mu       sync.RWMutex
	receiver Receiver
	senders  map[string]Sender
}

// NewRouter for routing rtp/rtcp packets
func NewRouter(tid string, recv Receiver) *Router {
	r := &Router{
		tid:      tid,
		receiver: recv,
		senders:  make(map[string]Sender),
	}

	go r.start()

	return r
}

// Track returns the router receiver track
func (r *Router) Track() Track {
	return r.receiver.Track()
}

// AddSender to router
func (r *Router) AddSender(pid string, sub Sender) {
	r.mu.Lock()
	r.senders[pid] = sub
	r.mu.Unlock()

	go r.subFeedbackLoop(sub)
}

// DelSub to router
func (r *Router) DelSub(pid string) {
	r.mu.Lock()
	delete(r.senders, pid)
	r.mu.Unlock()
}

// Close a router
func (r *Router) Close() {
	log.Debugf("Router close")
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stop = true

	// Close senders
	for pid, sub := range r.senders {
		sub.Close()
		delete(r.senders, pid)
	}
	r.receiver.Close()
}

func (r *Router) start() {
	defer util.Recover("[Router.start]")
	for {
		r.mu.RLock()
		if r.stop {
			r.mu.RUnlock()
			return
		}
		r.mu.RUnlock()

		pkt, err := r.receiver.ReadRTP()

		if err != nil {
			log.Errorf("r.receiver.ReadRTP err=%v", err)
			continue
		}
		if pkt == nil {
			continue
		}

		// Push to sub send queues
		r.mu.RLock()
		for _, sub := range r.senders {
			sub.WriteRTP(pkt)
		}
		r.mu.RUnlock()
	}
}

// subFeedbackLoop reads rtcp packets from the sub
// and either handles them or forwards them to the receiver.
func (r *Router) subFeedbackLoop(sub Sender) {
	for {
		r.mu.RLock()
		if r.stop {
			r.mu.RUnlock()
			return
		}
		r.mu.RUnlock()

		pkt, err := sub.ReadRTCP()

		if err != nil {
			log.Errorf("sub nil rtcp packet")
			return
		}

		switch pkt := pkt.(type) {
		case *rtcp.TransportLayerNack:
			log.Infof("Router got nack: %+v", pkt)
			for _, pair := range pkt.Nacks {
				bufferpkt := r.receiver.GetPacket(pair.PacketID)
				if bufferpkt != nil {
					// We found the packet in the buffer, resend to sub
					sub.WriteRTP(bufferpkt)
					continue
				}

				// Packet not found, request from receiver
				nack := &rtcp.TransportLayerNack{
					//origin ssrc
					SenderSSRC: pkt.SenderSSRC,
					MediaSSRC:  pkt.MediaSSRC,
					Nacks:      []rtcp.NackPair{{PacketID: pair.PacketID}},
				}
				err = r.receiver.WriteRTCP(nack)
				if err != nil {
					log.Errorf("Error writing nack RTCP %s", err)
				}
			}
		default:
			err = r.receiver.WriteRTCP(pkt)
			if err != nil {
				log.Errorf("Error writing RTCP %s", err)
			}
		}
	}
}

func (r *Router) stats() string {
	info := fmt.Sprintf("    router: %d | %s\n", r.receiver.Track().SSRC(), r.receiver.stats())

	if len(r.senders) < 6 {
		for pid, sub := range r.senders {
			info += fmt.Sprintf("      sender: %s | %s\n", pid, sub.stats())
		}
		info += "\n"
	} else {
		info += fmt.Sprintf("      senders: %d\n\n", len(r.senders))
	}

	return info
}
