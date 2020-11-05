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
	closed         bool
}

// NewSession creates a new session
func NewSession(id string) *Session {
	return &Session{
		id:         id,
		transports: make(map[string]Transport),
		closed:     false,
	}
}

// AddTransport adds a transport to the session
func (s *Session) AddTransport(transport Transport) {
	s.mu.Lock()
	s.transports[transport.ID()] = transport
	s.mu.Unlock()
}

// RemoveTransport removes a transport from the session
func (s *Session) RemoveTransport(tid string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Infof("RemoveTransport %s from session %s", tid, s.id)
	delete(s.transports, tid)

	// Close session if no transports
	if len(s.transports) == 0 && s.onCloseHandler != nil && !s.closed {
		s.onCloseHandler()
		s.closed = true
	}
}

// Publish will add a Sender to all peers in current Session from given
// Receiver
func (s *Session) Publish(router Router, rr *receiverRouter) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for tid, t := range s.transports {
		// Don't sub to self
		if router.ID() == tid {
			continue
		}

		log.Infof("Publish mediaSSRC to %s", tid)

		if t, ok := t.(*WebRTCTransport); ok {
			if err := router.AddSender(t, rr); err != nil {
				log.Errorf("Error subscribing transport to router: %s", err)
				continue
			}
		}
	}
}

// Subscribe will create a Sender for every other Receiver in the session
func (s *Session) Subscribe(p *WebRTCTransport) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.transports {
		if t.ID() == p.id {
			continue
		}
		err := t.GetRouter().AddSender(p, nil)
		if err != nil {
			log.Errorf("Subscribing to router err: %v", err)
			continue
		}
	}
}

// Transports returns transports in this session
func (s *Session) Transports() map[string]Transport {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.transports
}

// OnClose is called when the session is closed
func (s *Session) OnClose(f func()) {
	s.onCloseHandler = f
}
