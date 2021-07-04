package sfu

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/pion/webrtc/v3"
)

// Session represents a set of peers. Transports inside a SessionLocal
// are automatically subscribed to each other.
type Session interface {
	ID() string
	Publish(router Router, r Receiver)
	Subscribe(peer Peer)
	AddPeer(peer Peer)
	RemovePeer(peer Peer)
	AudioObserver() *AudioObserver
	AddDatachannel(owner string, dc *webrtc.DataChannel)
	GetDCMiddlewares() []*Datachannel
	GetDataChannelLabels() []string
	GetDataChannels(origin, label string) (dcs []*webrtc.DataChannel)
	Peers() []Peer
}

type SessionLocal struct {
	id             string
	mu             sync.RWMutex
	peers          map[string]Peer
	closed         atomicBool
	audioObs       *AudioObserver
	fanOutDCs      []string
	datachannels   []*Datachannel
	onCloseHandler func()
}

const (
	AudioLevelsMethod = "audioLevels"
)

// NewSession creates a new SessionLocal
func NewSession(id string, dcs []*Datachannel, cfg WebRTCTransportConfig) Session {
	s := &SessionLocal{
		id:           id,
		peers:        make(map[string]Peer),
		datachannels: dcs,
		audioObs:     NewAudioObserver(cfg.Router.AudioLevelThreshold, cfg.Router.AudioLevelInterval, cfg.Router.AudioLevelFilter),
	}
	go s.audioLevelObserver(cfg.Router.AudioLevelInterval)
	return s
}

// ID return SessionLocal id
func (s *SessionLocal) ID() string {
	return s.id
}

func (s *SessionLocal) AudioObserver() *AudioObserver {
	return s.audioObs
}

func (s *SessionLocal) GetDCMiddlewares() []*Datachannel {
	return s.datachannels
}

func (s *SessionLocal) GetDataChannelLabels() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, 0, len(s.datachannels)+len(s.fanOutDCs))
	copy(res, s.fanOutDCs)
	for _, dc := range s.datachannels {
		res = append(res, dc.Label)
	}
	return res
}

func (s *SessionLocal) AddPeer(peer Peer) {
	s.mu.Lock()
	s.peers[peer.ID()] = peer
	s.mu.Unlock()
}

// RemovePeer removes a transport from the SessionLocal
func (s *SessionLocal) RemovePeer(p Peer) {
	pid := p.ID()
	Logger.V(0).Info("RemovePeer from SessionLocal", "peer_id", pid, "session_id", s.id)
	s.mu.Lock()
	if s.peers[pid] == p {
		delete(s.peers, pid)
	}
	peerCount := len(s.peers)
	s.mu.Unlock()

	// Close SessionLocal if no peers
	if peerCount == 0 {
		s.Close()
	}
}

func (s *SessionLocal) AddDatachannel(owner string, dc *webrtc.DataChannel) {
	label := dc.Label()

	s.mu.Lock()
	s.fanOutDCs = append(s.fanOutDCs, label)
	peerOwner := s.peers[owner]
	peers := make([]Peer, 0, len(s.peers))
	for _, p := range s.peers {
		if p == peerOwner || p.Subscriber() == nil {
			continue
		}
		peers = append(peers, p)
	}
	s.mu.Unlock()
	peerOwner.Subscriber().RegisterDatachannel(label, dc)

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

// Publish will add a Sender to all peers in current SessionLocal from given
// Receiver
func (s *SessionLocal) Publish(router Router, r Receiver) {
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

// Subscribe will create a Sender for every other Receiver in the SessionLocal
func (s *SessionLocal) Subscribe(peer Peer) {
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

// Peers returns peers in this SessionLocal
func (s *SessionLocal) Peers() []Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := make([]Peer, 0, len(s.peers))
	for _, peer := range s.peers {
		p = append(p, peer)
	}
	return p
}

// OnClose is called when the SessionLocal is closed
func (s *SessionLocal) OnClose(f func()) {
	s.onCloseHandler = f
}

func (s *SessionLocal) Close() {
	if !s.closed.set(true) {
		return
	}
	if s.onCloseHandler != nil {
		s.onCloseHandler()
	}
}

func (s *SessionLocal) setRelayedDatachannel(peerID string, datachannel *webrtc.DataChannel) {
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

func (s *SessionLocal) audioLevelObserver(audioLevelInterval int) {
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

		msg := ChannelAPIMessage{
			Method: AudioLevelsMethod,
			Params: levels,
		}

		l, err := json.Marshal(&msg)
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

func (s *SessionLocal) onMessage(origin, label string, msg webrtc.DataChannelMessage) {
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

func (s *SessionLocal) GetDataChannels(origin, label string) []*webrtc.DataChannel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dcs := make([]*webrtc.DataChannel, 0, len(s.peers))
	for pid, p := range s.peers {
		if origin == pid || p.Subscriber() == nil {
			continue
		}

		if dc := p.Subscriber().DataChannel(label); dc != nil && dc.ReadyState() == webrtc.DataChannelStateOpen {
			dcs = append(dcs, dc)
		}
	}
	return dcs
}
