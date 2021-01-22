package sfu

import (
	"sync"

	log "github.com/pion/ion-log"
	"github.com/pion/webrtc/v3"
)

// Session represents a set of peers. Transports inside a session
// are automatically subscribed to each other.
type Session struct {
	id             string
	mu             sync.RWMutex
	peers          map[string]*Peer
	settings       *Settings
	onCloseHandler func()
	closed         bool
}

// NewSession creates a new session
func NewSession(id string, s *Settings) *Session {
	return &Session{
		id:       id,
		peers:    make(map[string]*Peer),
		closed:   false,
		settings: s,
	}
}

// AddPublisher adds a transport to the session
func (s *Session) AddPeer(peer *Peer) {
	s.mu.Lock()
	s.peers[peer.id] = peer
	s.mu.Unlock()
}

// RemovePeer removes a transport from the session
func (s *Session) RemovePeer(pid string) {
	s.mu.Lock()
	log.Infof("RemovePeer %s from session %s", pid, s.id)
	delete(s.peers, pid)
	s.mu.Unlock()

	// Close session if no peers
	if len(s.peers) == 0 && s.onCloseHandler != nil && !s.closed {
		s.onCloseHandler()
		s.closed = true
	}
}

func (s *Session) onMessage(origin, label string, msg webrtc.DataChannelMessage) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for pid, p := range s.peers {
		if origin == pid {
			continue
		}

		dc := p.Subscriber.channels[label]
		if dc != nil && dc.ReadyState() == webrtc.DataChannelStateOpen {
			if msg.IsString {
				if err := dc.SendText(string(msg.Data)); err != nil {
					log.Errorf("Sending dc message err: %v", err)
				}
			} else {
				if err := dc.Send(msg.Data); err != nil {
					log.Errorf("Sending dc message err: %v", err)
				}
			}
		}
	}
}

func (s *Session) AddDatachannel(owner string, dc *webrtc.DataChannel) {
	label := dc.Label()

	s.mu.RLock()
	defer s.mu.RUnlock()

	peer := s.peers[owner]
	peer.Subscriber.channels[label] = dc
	var (
		process        MessageProcessor
		middlewares    []func(p MessageProcessor) MessageProcessor
		withMiddleware bool
	)

	if s.settings.FanOutDatachannelMiddlewares != nil {
		middlewares, withMiddleware = s.settings.FanOutDatachannelMiddlewares[dc.Label()]
	}

	if withMiddleware {
		ch := NewDCChain(middlewares)
		process = ch.Process(ProcessFunc(func(_ *Peer, msg webrtc.DataChannelMessage) {
			s.onMessage(owner, label, msg)
		}))
	}

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if !withMiddleware {
			s.onMessage(owner, label, msg)
		} else {
			process.Process(peer, msg)
		}
	})

	for pid, p := range s.peers {
		// Don't add to self
		if owner == pid {
			continue
		}
		n, err := p.Subscriber.AddDataChannel(label)

		if err != nil {
			log.Errorf("error adding datachannel: %s", err)
			continue
		}

		pid := pid
		n.OnMessage(func(msg webrtc.DataChannelMessage) {
			s.onMessage(pid, label, msg)
		})

		p.Subscriber.negotiate()
	}
}

// Publish will add a Sender to all peers in current Session from given
// Receiver
func (s *Session) Publish(router Router, r Receiver) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for pid, p := range s.peers {
		// Don't sub to self
		if router.ID() == pid {
			continue
		}

		log.Infof("Publishing track to peer %s", pid)

		if err := router.AddDownTracks(p.Subscriber, r); err != nil {
			log.Errorf("Error subscribing transport to router: %s", err)
			continue
		}
	}
}

// Subscribe will create a Sender for every other Receiver in the session
func (s *Session) Subscribe(peer *Peer) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	subdChans := false
	for pid, p := range s.peers {
		if pid == peer.id {
			continue
		}
		err := p.Publisher.GetRouter().AddDownTracks(peer.Subscriber, nil)
		if err != nil {
			log.Errorf("Subscribing to router err: %v", err)
			continue
		}

		if !subdChans {
			for _, dc := range p.Subscriber.channels {
				label := dc.Label()
				n, err := peer.Subscriber.AddDataChannel(label)

				if err != nil {
					log.Errorf("error adding datachannel: %s", err)
					continue
				}

				n.OnMessage(func(msg webrtc.DataChannelMessage) {
					s.onMessage(peer.id, label, msg)
				})
			}
			subdChans = true

			peer.Subscriber.negotiate()
		}
	}
}

// Transports returns peers in this session
func (s *Session) Peers() map[string]*Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.peers
}

// OnClose is called when the session is closed
func (s *Session) OnClose(f func()) {
	s.onCloseHandler = f
}
