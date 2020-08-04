package sfu

import (
	"sync"
	"time"

	"github.com/pion/webrtc/v3"

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

// ReceiverConfig defines receiver configurations
type ReceiverConfig struct {
	Video WebRTCVideoReceiverConfig `mapstructure:"video"`
}

// Config for base SFU
type Config struct {
	WebRTC   WebRTCConfig   `mapstructure:"webrtc"`
	Log      log.Config     `mapstructure:"log"`
	Receiver ReceiverConfig `mapstructure:"receiver"`
}

var (
	// only support unified plan
	cfg = webrtc.Configuration{
		SDPSemantics: webrtc.SDPSemanticsUnifiedPlanWithFallback,
	}

	config  Config
	setting webrtc.SettingEngine
)

// SFU represents an sfu instance
type SFU struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSFU creates a new sfu instance
func NewSFU(c Config) *SFU {
	s := &SFU{
		sessions: make(map[string]*Session),
	}

	config = c

	log.Init(c.Log.Level)

	var icePortStart, icePortEnd uint16

	if len(c.WebRTC.ICEPortRange) == 2 {
		icePortStart = c.WebRTC.ICEPortRange[0]
		icePortEnd = c.WebRTC.ICEPortRange[1]
	}

	if icePortStart != 0 || icePortEnd != 0 {
		if err := setting.SetEphemeralUDPPortRange(icePortStart, icePortEnd); err != nil {
			panic(err)
		}
	}

	var iceServers []webrtc.ICEServer
	for _, iceServer := range c.WebRTC.ICEServers {
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

// NewSession creates a new session instance
func (s *SFU) NewSession(id string) *Session {
	session := NewSession(id)
	session.OnClose(func() {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
	})

	s.mu.Lock()
	s.sessions[id] = session
	s.mu.Unlock()
	return session
}

// GetSession by id
func (s *SFU) GetSession(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

func (s *SFU) stats() {
	t := time.NewTicker(statCycle)
	for range t.C {
		info := "\n----------------stats-----------------\n"

		s.mu.RLock()
		if len(s.sessions) == 0 {
			s.mu.RUnlock()
			continue
		}

		for _, session := range s.sessions {
			info += session.stats()
		}
		s.mu.RUnlock()
		log.Infof(info)
	}
}
