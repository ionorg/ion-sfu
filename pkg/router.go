package sfu

import (
	"sync"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/util"
	"github.com/pion/rtp"
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
	return &Router{
		pub:  recv,
		subs: make(map[string]*Sender),
	}
}

// AddSub to router
func (r *Router) AddSub(pid string, sub *Sender) {
	r.subsLock.Lock()
	r.subs[pid] = sub
	r.subsLock.Unlock()
}

func (r *Router) start() {
	defer util.Recover("[Router.start]")
	for {
		if r.stop {
			return
		}

		var pkt *rtp.Packet
		var err error
		// get rtp from pub
		pkt, err = r.pub.ReadRTP()
		if err != nil {
			log.Errorf("r.pub.ReadRTP err=%v", err)
			continue
		}
		// log.Debugf("pkt := <-r.subCh %v", pkt)
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
