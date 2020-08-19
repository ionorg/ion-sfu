package sfu

import (
	"fmt"
	"sync"

	"github.com/pion/ion-sfu/pkg/log"
)

// Session represents a set of transports. Transports inside a session
// are automatically subscribed to each other.
type Session struct {
	id             string
	transports     map[string]Transport
	broadcasts     map[string]chan interface{}
	mu             sync.RWMutex
	onCloseHandler func()
}

// NewSession creates a new session
func NewSession(id string) *Session {
	return &Session{
		id:         id,
		transports: make(map[string]Transport),
		broadcasts: make(map[string]chan interface{}),
	}
}

// AddTransport adds a transport to the session
func (r *Session) AddTransport(transport Transport) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.transports[transport.ID()] = transport
}

// AddWebsocket adds a websocket connection to the session
func (r *Session) AddBroadcast(id string, broadcast chan interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.broadcasts[id] = broadcast
}

// AddWebsocket adds a websocket connection to the session
func (r *Session) RemoveBroadcast(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.broadcasts, id)
}

// Broadcast broadcasts a signal message to all websockets connected to the session
func (r *Session) Broadcast(msg interface{}) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, ch := range r.broadcasts {
		select {
		case ch <- msg:
			log.Debugf("Wrote broadcast: %#v", msg)
		}
	}

	return nil
}

// RemoveTransport removes a transport for the session
func (r *Session) RemoveTransport(tid string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.transports, tid)

	// Close session if no transports
	if len(r.transports) == 0 && r.onCloseHandler != nil {
		r.onCloseHandler()
	}
}

// AddRouter adds a router to transports
func (r *Session) AddRouter(router *Router) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for tid, t := range r.transports {
		// Don't sub to self
		if router.tid == tid {
			continue
		}

		log.Infof("AddRouter ssrc %d to %s", router.Track().SSRC(), tid)

		sender, err := t.NewSender(router.Track())

		if err != nil {
			log.Errorf("Error subscribing transport to router: %s", err)
			continue
		}

		// Attach sender to source
		router.AddSender(tid, sender)
	}
}

// Transports returns transports in this session
func (r *Session) Transports() map[string]Transport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.transports
}

// OnClose called when session is closed
func (r *Session) OnClose(f func()) {
	r.onCloseHandler = f
}

func (r *Session) stats() string {
	info := fmt.Sprintf("\nsession: %s\n", r.id)

	r.mu.RLock()
	for _, transport := range r.transports {
		info += transport.stats()
	}
	r.mu.RUnlock()

	return info
}
