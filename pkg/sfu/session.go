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
	closed         bool
	fanOutDCs      []string
	datachannels   []*Datachannel
	onCloseHandler func()
}

// NewSession creates a new session
func NewSession(id string, dcs []*Datachannel) *Session {
	return &Session{
		id:           id,
		peers:        make(map[string]*Peer),
		closed:       false,
		datachannels: dcs,
	}
}

func (s *Session) AddDataChannel(dc *Datachannel) {
	s.mu.Lock()
	s.datachannels = append(s.datachannels, dc)
	s.mu.Unlock()
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

		dc := p.subscriber.channels[label]
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

func (s *Session) getDataChannels(origin, label string) (dcs []*webrtc.DataChannel) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for pid, p := range s.peers {
		if origin == pid {
			continue
		}

		if dc, ok := p.subscriber.channels[label]; ok {
			dcs = append(dcs, dc)
		}
	}
	return
}

func (s *Session) AddDatachannel(owner string, dc *webrtc.DataChannel) {
	label := dc.Label()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.fanOutDCs = append(s.fanOutDCs, label)
	s.peers[owner].subscriber.channels[label] = dc

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		s.onMessage(owner, label, msg)
	})

	for pid, p := range s.peers {
		// Don't add to self
		if owner == pid {
			continue
		}
		n, err := p.subscriber.AddDataChannel(label)

		if err != nil {
			log.Errorf("error adding datachannel: %s", err)
			continue
		}

		pid := pid
		n.OnMessage(func(msg webrtc.DataChannelMessage) {
			s.onMessage(pid, label, msg)
		})

		go p.subscriber.negotiate()
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

		if err := router.AddDownTracks(p.subscriber, r); err != nil {
			log.Errorf("Error subscribing transport to router: %s", err)
			continue
		}
	}
}

// Subscribe will create a Sender for every other Receiver in the session
func (s *Session) Subscribe(peer *Peer) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Subscribe to fan out datachannels
	for _, label := range s.fanOutDCs {
		n, err := peer.subscriber.AddDataChannel(label)

		if err != nil {
			log.Errorf("error adding datachannel: %s", err)
			continue
		}

		label := label
		n.OnMessage(func(msg webrtc.DataChannelMessage) {
			s.onMessage(peer.id, label, msg)
		})
	}

	// Subscribe to publisher streams
	for pid, p := range s.peers {
		if pid == peer.id {
			continue
		}
		err := p.publisher.GetRouter().AddDownTracks(peer.subscriber, nil)
		if err != nil {
			log.Errorf("Subscribing to router err: %v", err)
			continue
		}
	}

	peer.subscriber.negotiate()
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
