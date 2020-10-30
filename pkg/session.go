package sfu

import (
	"sync"

	log "github.com/pion/ion-log"
)

// Session represents a set of transports. Transports inside a session
// are automatically subscribed to each other.
type Session struct {
	id             string
	mu             sync.RWMutex
	transports     map[string]Transport
	onCloseHandler func()
}

// NewSession creates a new session
func NewSession(id string) *Session {
	return &Session{
		id:         id,
		transports: make(map[string]Transport),
	}
}

// AddTransport adds a transport to the session
func (r *Session) AddTransport(transport Transport) {
	r.mu.Lock()
	r.transports[transport.ID()] = transport
	r.mu.Unlock()
}

// RemoveTransport removes a transport from the session
func (r *Session) RemoveTransport(tid string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	log.Infof("RemoveTransport %s from session %s", tid, r.id)
	delete(r.transports, tid)

	// Close session if no transports
	if len(r.transports) == 0 && r.onCloseHandler != nil {
		r.onCloseHandler()
	}
}

// AddRouter adds a router to transports
func (r *Session) AddRouter(router Router, rr *receiverRouter) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for tid, t := range r.transports {
		// Don't sub to self
		if router.ID() == tid {
			continue
		}

		log.Infof("AddRouter mediaSSRC to %s", tid)

		if t, ok := t.(*WebRTCTransport); ok {
			if err := router.AddSender(t, rr); err != nil {
				log.Errorf("Error subscribing transport to router: %s", err)
				continue
			}
		}
	}
}

// Transports returns transports in this session
func (r *Session) Transports() map[string]Transport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.transports
}

// OnClose is called when the session is closed
func (r *Session) OnClose(f func()) {
	r.onCloseHandler = f
}
