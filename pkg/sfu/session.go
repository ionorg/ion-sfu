package sfu

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/pion/ion-sfu/pkg/buffer"
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
	bufferFactory  *buffer.Factory
	onCloseHandler func()
}

// NewSession creates a new session
func NewSession(id string, bf *buffer.Factory, dcs []*Datachannel, cfg WebRTCTransportConfig) *Session {
	s := &Session{
		id:            id,
		peers:         make(map[string]*Peer),
		datachannels:  dcs,
		bufferFactory: bf,
		audioObserver: newAudioLevel(cfg.router.AudioLevelThreshold, cfg.router.AudioLevelInterval, cfg.router.AudioLevelFilter),
	}
	go s.audioLevelObserver(cfg.router.AudioLevelInterval)
	return s

}

// ID return session id
func (s *Session) ID() string {
	return s.id
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
	Logger.V(0).Info("RemovePeer from session", "peer_id", pid, "session_id", s.id)
	delete(s.peers, pid)
	s.mu.Unlock()

	// Close session if no peers
	if len(s.peers) == 0 && s.onCloseHandler != nil && !s.closed.get() {
		s.closed.set(true)
		s.onCloseHandler()
	}
}

func (s *Session) AddDatachannel(owner string, dc *webrtc.DataChannel) {
	label := dc.Label()

	s.mu.Lock()
	s.fanOutDCs = append(s.fanOutDCs, label)
	s.peers[owner].subscriber.channels[label] = dc
	peers := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		if p.id == owner || p.subscriber == nil {
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
			Logger.Error(err, "error adding datachannel")
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
		if router.ID() == p.id || p.subscriber == nil {
			continue
		}

		Logger.V(0).Info("Publishing track to peer", "peer_id", p.id)

		if err := router.AddDownTracks(p.subscriber, r); err != nil {
			Logger.Error(err, "Error subscribing transport to router")
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
		if p == peer || p.publisher == nil {
			continue
		}
		peers = append(peers, p)
	}
	s.mu.RUnlock()

	// Subscribe to fan out datachannels
	for _, label := range fdc {
		n, err := peer.subscriber.AddDataChannel(label)
		if err != nil {
			Logger.Error(err, "error adding datachannel")
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
			Logger.Error(err, "Subscribing to router err")
			continue
		}
	}

	peer.subscriber.negotiate()
}

// BufferFactory returns current session buffer factory
func (s *Session) BufferFactory() *buffer.Factory {
	return s.bufferFactory
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

func (s *Session) setRelayedDatachannel(peerID string, datachannel *webrtc.DataChannel) {
	label := datachannel.Label()
	for _, dc := range s.datachannels {
		dc := dc
		if dc.Label == label {
			mws := newDCChain(dc.middlewares)
			p := mws.Process(ProcessFunc(func(ctx context.Context, args ProcessArgs) {
				if dc.onMessage != nil {
					dc.onMessage(ctx, args, s.getDataChannels(peerID, dc.Label))
				}
			}))
			s.mu.RLock()
			peer := s.peers[peerID]
			s.mu.RUnlock()
			datachannel.OnMessage(func(msg webrtc.DataChannelMessage) {
				p.Process(context.Background(), ProcessArgs{
					Peer:        peer,
					Message:     msg,
					DataChannel: datachannel,
				})
			})
		}
		return
	}

	datachannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		s.onMessage(peerID, label, msg)
	})
}

func (s *Session) audioLevelObserver(audioLevelInterval int) {
	if audioLevelInterval <= 50 {
		Logger.V(0).Info("Values near/under 20ms may return unexpected values")
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
			Logger.Error(err, "Marshaling audio levels err")
			continue
		}

		sl := string(l)
		dcs := s.getDataChannels("", APIChannelLabel)

		for _, ch := range dcs {
			if err = ch.SendText(sl); err != nil {
				Logger.Error(err, "Sending audio levels err")
			}
		}
	}
}

func (s *Session) onMessage(origin, label string, msg webrtc.DataChannelMessage) {
	dcs := s.getDataChannels(origin, label)
	for _, dc := range dcs {
		if msg.IsString {
			if err := dc.SendText(string(msg.Data)); err != nil {
				Logger.Error(err, "Sending dc message err")
			}
		} else {
			if err := dc.Send(msg.Data); err != nil {
				Logger.Error(err, "Sending dc message err")
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

func (s *Session) getDataChannelLabels() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, 0, len(s.datachannels)+len(s.fanOutDCs))
	copy(res, s.fanOutDCs)
	for _, dc := range s.datachannels {
		res = append(res, dc.Label)
	}
	return res
}
