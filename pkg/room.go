package sfu

import (
	"fmt"
	"sync"

	"github.com/pion/ion-sfu/pkg/log"
)

// Room represents a set of transports. Transports inside a room
// are automatically subscribed to each other.
type Room struct {
	id             string
	transports     map[string]Transport
	transportsLock sync.RWMutex
	onCloseHandler func()
}

// NewRoom creates a new room
func NewRoom(id string) *Room {
	return &Room{
		id:         id,
		transports: make(map[string]Transport),
	}
}

// AddTransport adds a transport to the room
func (r *Room) AddTransport(transport Transport) {
	r.transportsLock.Lock()
	defer r.transportsLock.Unlock()

	// Subscribe new transport to existing transports
	for _, t := range r.transports {
		t.AddSub(transport)
	}

	r.transports[transport.ID()] = transport

	transport.OnClose(func() {
		r.transportsLock.Lock()
		defer r.transportsLock.Unlock()

		delete(r.transports, transport.ID())
		// Remove transport subs from pubs
		for _, t := range r.transports {
			for _, router := range t.Routers() {
				router.DelSub(transport.ID())
			}
		}

		// Close room if no transports
		if len(r.transports) == 0 && r.onCloseHandler != nil {
			r.onCloseHandler()
		}
	})

	// New track router added to transport, subscribe
	// other transports in room to it
	transport.OnRouter(func(router *Router) {
		r.transportsLock.Lock()
		defer r.transportsLock.Unlock()

		log.Debugf("on router %v", router)
		for tid, t := range r.transports {
			// Don't sub to self
			if transport.ID() == tid {
				continue
			}
			err := t.Subscribe(router, true)
			if err != nil {
				log.Errorf("Error subscribing transport to router: %s", err)
			}
		}
	})
}

// OnClose called when room is closed
func (r *Room) OnClose(f func()) {
	r.onCloseHandler = f
}

func (r *Room) stats() string {
	info := fmt.Sprintf("\nroom: %s\n", r.id)

	r.transportsLock.RLock()
	for _, transport := range r.transports {
		info += transport.stats()
	}
	r.transportsLock.RUnlock()

	return info
}
