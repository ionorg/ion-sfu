package sfu

import (
	"fmt"
	"io"
	"sync"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

// Router defines a track rtp/rtcp router
type Router struct {
	tid            string
	mu             sync.RWMutex
	onCloseHandler func()
	receiver       Receiver
	senders        map[string]Sender
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
func (r *Router) Track() *webrtc.Track {
	return r.receiver.Track()
}

// AddSender to router
func (r *Router) AddSender(pid string, sub Sender) {
	r.mu.Lock()
	r.senders[pid] = sub
	r.mu.Unlock()

	go r.subFeedbackLoop(pid, sub)
}

// OnClose is called when the router is closed
func (r *Router) OnClose(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onCloseHandler = f
}

// Close a router
func (r *Router) close() {
	log.Debugf("Router close")
	r.mu.Lock()
	defer r.mu.Unlock()

	// Close senders
	for pid, sub := range r.senders {
		sub.Close()
		delete(r.senders, pid)
	}
	r.receiver.Close()

	if r.onCloseHandler != nil {
		r.onCloseHandler()
	}
}

func (r *Router) start() {
	for {
		pkt, err := r.receiver.ReadRTP()

		if err == io.EOF {
			r.close()
			return
		}

		if err != nil {
			log.Errorf("r.receiver.ReadRTP err=%v", err)
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
func (r *Router) subFeedbackLoop(pid string, sub Sender) {
	defer func() {
		r.mu.Lock()
		delete(r.senders, pid)
		r.mu.Unlock()
	}()

	for {
		pkt, err := sub.ReadRTCP()
		if err == io.ErrClosedPipe {
			return
		}

		if err != nil {
			log.Errorf("read rtcp err %s", err)
			return
		}

		switch pkt := pkt.(type) {
		case *rtcp.TransportLayerNack:
			log.Tracef("Router got nack: %+v", pkt)
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
	info := fmt.Sprintf("    track id: %s label: %s ssrc: %d | %s\n", r.receiver.Track().ID(), r.receiver.Track().Label(), r.receiver.Track().SSRC(), r.receiver.stats())

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
