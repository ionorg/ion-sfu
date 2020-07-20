package sfu

import (
	"sync"
	"time"

	"github.com/pion/webrtc/v2"

	"github.com/pion/ion-sfu/pkg/log"
)

// ICEServerConfig defines parameters for ice servers
type ICEServerConfig struct {
	URLs       []string `mapstructure:"urls"`
	Username   string   `mapstructure:"username"`
	Credential string   `mapstructure:"credential"`
}

// WebRTCConfig defines parameters for ice
type WebRTCConfig struct {
	ICEPortRange []uint16          `mapstructure:"portrange"`
	ICEServers   []ICEServerConfig `mapstructure:"iceserver"`
}

// Config for base SFU
type Config struct {
	WebRTC WebRTCConfig `mapstructure:"webrtc"`
	Log    log.Config   `mapstructure:"log"`
}

var (
	// only support unified plan
	cfg = webrtc.Configuration{
		SDPSemantics: webrtc.SDPSemanticsUnifiedPlanWithFallback,
	}

	setting webrtc.SettingEngine
)

// SFU represents an sfu instance
type SFU struct {
	peers    map[string]*Peer
	peerLock sync.RWMutex
}

// NewSFU creates a new sfu instance
func NewSFU(config Config) *SFU {
	s := &SFU{
		peers: make(map[string]*Peer),
	}

	log.Init(config.Log.Level)

	var icePortStart, icePortEnd uint16

	if len(config.WebRTC.ICEPortRange) == 2 {
		icePortStart = config.WebRTC.ICEPortRange[0]
		icePortEnd = config.WebRTC.ICEPortRange[1]
	}

	if icePortStart != 0 || icePortEnd != 0 {
		if err := setting.SetEphemeralUDPPortRange(icePortStart, icePortEnd); err != nil {
			panic(err)
		}
	}

	var iceServers []webrtc.ICEServer
	for _, iceServer := range config.WebRTC.ICEServers {
		s := webrtc.ICEServer{
			URLs:       iceServer.URLs,
			Username:   iceServer.Username,
			Credential: iceServer.Credential,
		}
		iceServers = append(iceServers, s)
	}

	cfg.ICEServers = iceServers

	go s.stats()

	return s
}

// CreatePeer a webrtc stream
func (s *SFU) CreatePeer(offer webrtc.SessionDescription) (*Peer, webrtc.SessionDescription, error) {
	peer, err := NewPeer(offer)
	peer.OnClose(func() {
		s.peerLock.Lock()
		delete(s.peers, peer.id)
		s.peerLock.Unlock()
	})

	err = peer.SetRemoteDescription(offer)
	if err != nil {
		log.Errorf("Offer error: %v", err)
		return nil, webrtc.SessionDescription{}, err
	}

	answer, err := peer.CreateAnswer()
	if err != nil {
		log.Errorf("Offer error: answer=%v err=%v", answer, err)
		return nil, webrtc.SessionDescription{}, err
	}

	err = peer.SetLocalDescription(answer)
	if err != nil {
		log.Errorf("Offer error: answer=%v err=%v", answer, err)
		return nil, webrtc.SessionDescription{}, err
	}

	s.peerLock.Lock()
	s.peers[peer.id] = peer
	s.peerLock.Unlock()

	log.Debugf("Connect answer => %v", answer)
	return peer, answer, nil
}

func (s *SFU) stats() {
	t := time.NewTicker(statCycle)
	for range t.C {
		info := "\n----------------stats-----------------\n"

		s.peerLock.RLock()
		for _, peer := range s.peers {
			info += peer.GetStats()
		}
		s.peerLock.RUnlock()
		log.Infof(info)
	}
}
