package sfu

import (
	"fmt"
	"sync"

	"github.com/pion/ion-sfu/pkg/log"
)

// Session represents a set of transports. Transports inside a session
// are automatically subscribed to each other.
type Session struct {
	id             uint32
	transports     map[string]Transport
	transportsLock sync.RWMutex
	onCloseHandler func()
}

// NewSession creates a new session
func NewSession(id uint32) *Session {
	return &Session{
		id:         id,
		transports: make(map[string]Transport),
	}
}

// AddTransport adds a transport to the session
func (r *Session) AddTransport(transport Transport) {
	r.transportsLock.Lock()
	defer r.transportsLock.Unlock()

	r.transports[transport.ID()] = transport
}

// RemoveTransport removes a transport for the session
func (r *Session) RemoveTransport(tid string) {
	r.transportsLock.Lock()
	defer r.transportsLock.Unlock()

	delete(r.transports, tid)
	// Remove transport subs from pubs
	for _, t := range r.transports {
		for _, router := range t.Routers() {
			router.DelSub(tid)
		}
	}

	// Close session if no transports
	if len(r.transports) == 0 && r.onCloseHandler != nil {
		r.onCloseHandler()
	}
}

// AddRouter adds a router to existing transports
func (r *Session) AddRouter(router *Router) {
	r.transportsLock.Lock()
	defer r.transportsLock.Unlock()

	for tid, t := range r.transports {
		// Don't sub to self
		if router.tid == tid {
			continue
		}

		log.Infof("AddRouter ssrc %d to %s", router.Track().SSRC(), tid)

		sender, err := t.NewSender(router.Track())

		if err != nil {
			log.Errorf("Error subscribing transport to router: %s", err)
		}

		// Attach sender to source
		router.AddSender(tid, sender)

		// TODO: required until pion/webrtc supports OnNegotiationNeeded
		// (https://github.com/pion/webrtc/pull/1322)
		if t.(*WebRTCTransport).onNegotiationNeededHandler != nil {
			t.(*WebRTCTransport).onNegotiationNeededHandler()
		}
	}
}

// Transports returns transports in this session
func (r *Session) Transports() map[string]Transport {
	r.transportsLock.RLock()
	defer r.transportsLock.RUnlock()
	return r.transports
}

// OnClose called when session is closed
func (r *Session) OnClose(f func()) {
	r.onCloseHandler = f
}

func (r *Session) stats() string {
	info := fmt.Sprintf("\nsession: %d\n", r.id)

	r.transportsLock.RLock()
	for _, transport := range r.transports {
		info += transport.stats()
	}
	r.transportsLock.RUnlock()

	return info
}
