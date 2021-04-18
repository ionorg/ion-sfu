package sfu

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/pion/webrtc/v3"
)

// Session represents a set of peers. Transports inside a session
// are automatically subscribed to each other.
type Session interface {
	ID() string
	Publish(router Router, r Receiver)
	AudioObserver() *AudioObserver
	AddDatachannel(owner string, dc *webrtc.DataChannel)
	GetDataChannelLabels() []string
	GetDataChannels(origin, label string) (dcs []*webrtc.DataChannel)
}

type session struct {
	id             string
	mu             sync.RWMutex
	peers          map[string]Peer
	closed         atomicBool
	audioObs       *AudioObserver
	fanOutDCs      []string
	datachannels   []*Datachannel
	onCloseHandler func()
}

// NewSession creates a new session
func NewSession(id string, dcs []*Datachannel, cfg WebRTCTransportConfig) *session {
	s := &session{
		id:           id,
		peers:        make(map[string]Peer),
		datachannels: dcs,
		audioObs:     NewAudioObserver(cfg.Router.AudioLevelThreshold, cfg.Router.AudioLevelInterval, cfg.Router.AudioLevelFilter),
	}
	go s.audioLevelObserver(cfg.Router.AudioLevelInterval)
	return s

}

// ID return session id
func (s *session) ID() string {
	return s.id
}

func (s *session) AudioObserver() *AudioObserver {
	return s.audioObs
}

func (s *session) GetDataChannelLabels() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, 0, len(s.datachannels)+len(s.fanOutDCs))
	copy(res, s.fanOutDCs)
	for _, dc := range s.datachannels {
		res = append(res, dc.Label)
	}
	return res
}

func (s *session) AddPeer(peer Peer) {
	s.mu.Lock()
	s.peers[peer.ID()] = peer
	s.mu.Unlock()
}

// RemovePeer removes a transport from the session
func (s *session) RemovePeer(pid string) {
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

func (s *session) AddDatachannel(owner string, dc *webrtc.DataChannel) {
	label := dc.Label()

	s.mu.Lock()
	s.fanOutDCs = append(s.fanOutDCs, label)
	s.peers[owner].Subscriber().channels[label] = dc
	peers := make([]Peer, 0, len(s.peers))
	for _, p := range s.peers {
		if p.ID() == owner || p.Subscriber() == nil {
			continue
		}
		peers = append(peers, p)
	}
	s.mu.Unlock()

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		s.onMessage(owner, label, msg)
	})

	for _, p := range peers {
		n, err := p.Subscriber().AddDataChannel(label)

		if err != nil {
			Logger.Error(err, "error adding datachannel")
			continue
		}

		pid := p.ID()
		n.OnMessage(func(msg webrtc.DataChannelMessage) {
			s.onMessage(pid, label, msg)
		})

		p.Subscriber().negotiate()
	}
}

// Publish will add a Sender to all peers in current session from given
// Receiver
func (s *session) Publish(router Router, r Receiver) {
	peers := s.Peers()

	for _, p := range peers {
		// Don't sub to self
		if router.ID() == p.ID() || p.Subscriber() == nil {
			continue
		}

		Logger.V(0).Info("Publishing track to peer", "peer_id", p.ID())

		if err := router.AddDownTracks(p.Subscriber(), r); err != nil {
			Logger.Error(err, "Error subscribing transport to Router")
			continue
		}
	}
}

// Subscribe will create a Sender for every other Receiver in the session
func (s *session) Subscribe(peer Peer) {
	s.mu.RLock()
	fdc := make([]string, len(s.fanOutDCs))
	copy(fdc, s.fanOutDCs)
	peers := make([]Peer, 0, len(s.peers))
	for _, p := range s.peers {
		if p == peer || p.Publisher() == nil {
			continue
		}
		peers = append(peers, p)
	}
	s.mu.RUnlock()

	// Subscribe to fan out datachannels
	for _, label := range fdc {
		n, err := peer.Subscriber().AddDataChannel(label)
		if err != nil {
			Logger.Error(err, "error adding datachannel")
			continue
		}
		l := label
		n.OnMessage(func(msg webrtc.DataChannelMessage) {
			s.onMessage(peer.ID(), l, msg)
		})
	}

	// Subscribe to publisher streams
	for _, p := range peers {
		err := p.Publisher().GetRouter().AddDownTracks(peer.Subscriber(), nil)
		if err != nil {
			Logger.Error(err, "Subscribing to Router err")
			continue
		}
	}

	peer.Subscriber().negotiate()
}

// Peers returns peers in this session
func (s *session) Peers() []Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := make([]Peer, 0, len(s.peers))
	for _, peer := range s.peers {
		p = append(p, peer)
	}
	return p
}

// OnClose is called when the session is closed
func (s *session) OnClose(f func()) {
	s.onCloseHandler = f
}

func (s *session) setRelayedDatachannel(peerID string, datachannel *webrtc.DataChannel) {
	label := datachannel.Label()
	for _, dc := range s.datachannels {
		dc := dc
		if dc.Label == label {
			mws := newDCChain(dc.middlewares)
			p := mws.Process(ProcessFunc(func(ctx context.Context, args ProcessArgs) {
				if dc.onMessage != nil {
					dc.onMessage(ctx, args)
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

func (s *session) audioLevelObserver(audioLevelInterval int) {
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
		levels := s.audioObs.Calc()

		if levels == nil {
			continue
		}

		l, err := json.Marshal(&levels)
		if err != nil {
			Logger.Error(err, "Marshaling audio levels err")
			continue
		}

		sl := string(l)
		dcs := s.GetDataChannels("", APIChannelLabel)

		for _, ch := range dcs {
			if err = ch.SendText(sl); err != nil {
				Logger.Error(err, "Sending audio levels err")
			}
		}
	}
}

func (s *session) onMessage(origin, label string, msg webrtc.DataChannelMessage) {
	dcs := s.GetDataChannels(origin, label)
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

func (s *session) GetDataChannels(origin, label string) (dcs []*webrtc.DataChannel) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for pid, p := range s.peers {
		if origin == pid {
			continue
		}

		if dc, ok := p.Subscriber().channels[label]; ok && dc.ReadyState() == webrtc.DataChannelStateOpen {
			dcs = append(dcs, dc)
		}
	}
	return
}
