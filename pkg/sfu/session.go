package sfu

import (
	"encoding/json"
	"sync"
	"time"

	log "github.com/pion/ion-log"
	"github.com/pion/webrtc/v3"
)

// Session represents a set of peers. Transports inside a session
// are automatically subscribed to each other.
type Session struct {
	id             string
	mu             sync.RWMutex
	peers          map[string]*Peer
	closed         atomicBool
	fanOutDCs      []string
	datachannels   []*Datachannel
	audioObserver  *audioLevel
	onCloseHandler func()
}

// NewSession creates a new session
func NewSession(id string, dcs []*Datachannel, cfg WebRTCTransportConfig) *Session {
	s := &Session{
		id:            id,
		peers:         make(map[string]*Peer),
		datachannels:  dcs,
		audioObserver: newAudioLevel(cfg.router.AudioLevelThreshold, cfg.router.AudioLevelInterval, cfg.router.AudioLevelFilter),
	}
	go s.audioLevelObserver(cfg.router.AudioLevelInterval)
	return s

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
	if len(s.peers) == 0 && s.onCloseHandler != nil && !s.closed.get() {
		s.closed.set(true)
		s.onCloseHandler()
	}
}

func (s *Session) onMessage(origin, label string, msg webrtc.DataChannelMessage) {
	dcs := s.getDataChannels(origin, label)
	for _, dc := range dcs {
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

func (s *Session) getDataChannels(origin, label string) (dcs []*webrtc.DataChannel) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for pid, p := range s.peers {
		if origin == pid {
			continue
		}

		if dc, ok := p.subscriber.channels[label]; ok && dc.ReadyState() == webrtc.DataChannelStateOpen {
			dcs = append(dcs, dc)
		}
	}
	return
}

func (s *Session) AddDatachannel(owner string, dc *webrtc.DataChannel) {
	label := dc.Label()

	s.mu.Lock()
	s.fanOutDCs = append(s.fanOutDCs, label)
	s.peers[owner].subscriber.channels[label] = dc
	peers := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		if p.id == owner {
			continue
		}
		peers = append(peers, p)
	}
	s.mu.Unlock()

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		s.onMessage(owner, label, msg)
	})

	for _, p := range peers {
		n, err := p.subscriber.AddDataChannel(label)

		if err != nil {
			log.Errorf("error adding datachannel: %s", err)
			continue
		}

		pid := p.id
		n.OnMessage(func(msg webrtc.DataChannelMessage) {
			s.onMessage(pid, label, msg)
		})

		p.subscriber.negotiate()
	}
}

// Publish will add a Sender to all peers in current Session from given
// Receiver
func (s *Session) Publish(router Router, r Receiver) {
	peers := s.Peers()

	for _, p := range peers {
		// Don't sub to self
		if router.ID() == p.id {
			continue
		}

		log.Infof("Publishing track to peer %s", p.id)

		if err := router.AddDownTracks(p.subscriber, r); err != nil {
			log.Errorf("Error subscribing transport to router: %s", err)
			continue
		}
	}
}

// Subscribe will create a Sender for every other Receiver in the session
func (s *Session) Subscribe(peer *Peer) {
	s.mu.RLock()
	fdc := make([]string, len(s.fanOutDCs))
	copy(fdc, s.fanOutDCs)
	peers := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		if p == peer {
			continue
		}
		peers = append(peers, p)
	}
	s.mu.RUnlock()

	// Subscribe to fan out datachannels
	for _, label := range fdc {
		n, err := peer.subscriber.AddDataChannel(label)
		if err != nil {
			log.Errorf("error adding datachannel: %s", err)
			continue
		}
		l := label
		n.OnMessage(func(msg webrtc.DataChannelMessage) {
			s.onMessage(peer.id, l, msg)
		})
	}

	// Subscribe to publisher streams
	for _, p := range peers {
		err := p.publisher.GetRouter().AddDownTracks(peer.subscriber, nil)
		if err != nil {
			log.Errorf("Subscribing to router err: %v", err)
			continue
		}
	}

	peer.subscriber.negotiate()
}

// Transports returns peers in this session
func (s *Session) Peers() []*Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := make([]*Peer, 0, len(s.peers))
	for _, peer := range s.peers {
		p = append(p, peer)
	}
	return p
}

// OnClose is called when the session is closed
func (s *Session) OnClose(f func()) {
	s.onCloseHandler = f
}

func (s *Session) audioLevelObserver(audioLevelInterval int) {
	if audioLevelInterval <= 50 {
		log.Warnf("Values near/under 20ms may return unexpected values")
	}
	if audioLevelInterval == 0 {
		audioLevelInterval = 1000
	}
	for {
		time.Sleep(time.Duration(audioLevelInterval) * time.Millisecond)
		if s.closed.get() {
			return
		}
		levels := s.audioObserver.calc()

		if levels == nil {
			continue
		}

		l, err := json.Marshal(&levels)
		if err != nil {
			log.Errorf("Marshaling audio levels err: %v", err)
			continue
		}

		sl := string(l)
		dcs := s.getDataChannels("", APIChannelLabel)

		for _, ch := range dcs {
			if err = ch.SendText(sl); err != nil {
				log.Errorf("Sending audio levels err: %v", err)
			}
		}
	}
}

// ID return session id
func (s *Session) ID() string {
	return s.id
}
