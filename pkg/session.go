package sfu

import (
	"sync"

	log "github.com/pion/ion-log"
)

// Session represents a set of publishers. Transports inside a session
// are automatically subscribed to each other.
type Session struct {
	id             string
	mu             sync.RWMutex
	publishers     map[string]*Publisher
	subscribers    map[string]*Subscriber
	onCloseHandler func()
	closed         bool
}

// NewSession creates a new session
func NewSession(id string) *Session {
	return &Session{
		id:         id,
		publishers: make(map[string]*Publisher),
		closed:     false,
	}
}

// AddPublisher adds a transport to the session
func (s *Session) AddPublisher(publisher *Publisher) {
	s.mu.Lock()
	s.publishers[publisher.ID()] = publisher
	s.mu.Unlock()
}

// RemovePublisher removes a transport from the session
func (s *Session) RemovePublisher(tid string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Infof("RemovePublisher %s from session %s", tid, s.id)
	delete(s.publishers, tid)

	// Close session if no publishers
	if len(s.publishers) == 0 && s.onCloseHandler != nil && !s.closed {
		s.onCloseHandler()
		s.closed = true
	}
}

// Publish will add a Sender to all peers in current Session from given
// Receiver
func (s *Session) Publish(router Router, rr *receiverRouter) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for sid, s := range s.subscribers {
		// Don't sub to self
		if router.ID() == sid {
			continue
		}

		log.Infof("Publish mediaSSRC to %s", sid)

		if err := router.AddSender(s, rr); err != nil {
			log.Errorf("Error subscribing transport to router: %s", err)
			continue
		}
	}
}

// Subscribe will create a Sender for every other Receiver in the session
func (s *Session) Subscribe(sub *Subscriber) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, pub := range s.publishers {
		if pub.ID() == sub.id {
			continue
		}
		err := pub.GetRouter().AddSender(sub, nil)
		if err != nil {
			log.Errorf("Subscribing to router err: %v", err)
			continue
		}
	}
}

// Transports returns publishers in this session
func (s *Session) Publishers() map[string]*Publisher {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.publishers
}

// OnClose is called when the session is closed
func (s *Session) OnClose(f func()) {
	s.onCloseHandler = f
}
