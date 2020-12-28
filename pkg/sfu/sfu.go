package sfu

import (
	"math/rand"
	"runtime"
	"sync"
	"time"

	"github.com/pion/ion-sfu/pkg/buffer"

	"github.com/pion/webrtc/v3"

	log "github.com/pion/ion-log"
)

// ICEServerConfig defines parameters for ice servers
type ICEServerConfig struct {
	URLs       []string `mapstructure:"urls"`
	Username   string   `mapstructure:"username"`
	Credential string   `mapstructure:"credential"`
}

type Candidates struct {
	IceLite    bool     `mapstructure:"icelite"`
	NAT1To1IPs []string `mapstructure:"nat1to1"`
}

// WebRTCTransportConfig represents configuration options
type WebRTCTransportConfig struct {
	configuration webrtc.Configuration
	setting       webrtc.SettingEngine
	router        RouterConfig
}

// WebRTCConfig defines parameters for ice
type WebRTCConfig struct {
	ICEPortRange []uint16          `mapstructure:"portrange"`
	ICEServers   []ICEServerConfig `mapstructure:"iceserver"`
	Candidates   Candidates        `mapstructure:"candidates"`
	SDPSemantics string            `mapstructure:"sdpsemantics"`
}

// Config for base SFU
type Config struct {
	SFU struct {
		Ballast int64 `mapstructure:"ballast"`
	} `mapstructure:"sfu"`
	WebRTC WebRTCConfig `mapstructure:"webrtc"`
	Log    log.Config   `mapstructure:"log"`
	Router RouterConfig `mapstructure:"router"`
}

var (
	bufferFactory *buffer.Factory
	packetFactory *sync.Pool
)

// SFU represents an sfu instance
type SFU struct {
	webrtc   WebRTCTransportConfig
	router   RouterConfig
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewWebRTCTransportConfig parses our settings and returns a usable WebRTCTransportConfig for creating PeerConnections
func NewWebRTCTransportConfig(c Config) WebRTCTransportConfig {
	se := webrtc.SettingEngine{}

	var icePortStart, icePortEnd uint16

	if len(c.WebRTC.ICEPortRange) == 2 {
		icePortStart = c.WebRTC.ICEPortRange[0]
		icePortEnd = c.WebRTC.ICEPortRange[1]
	}

	if icePortStart != 0 || icePortEnd != 0 {
		if err := se.SetEphemeralUDPPortRange(icePortStart, icePortEnd); err != nil {
			panic(err)
		}
	}

	var iceServers []webrtc.ICEServer
	if c.WebRTC.Candidates.IceLite {
		se.SetLite(c.WebRTC.Candidates.IceLite)
	} else {
		for _, iceServer := range c.WebRTC.ICEServers {
			s := webrtc.ICEServer{
				URLs:       iceServer.URLs,
				Username:   iceServer.Username,
				Credential: iceServer.Credential,
			}
			iceServers = append(iceServers, s)
		}
	}

	se.BufferFactory = bufferFactory.GetOrNew

	sdpSemantics := webrtc.SDPSemanticsUnifiedPlan
	switch c.WebRTC.SDPSemantics {
	case "unified-plan-with-fallback":
		sdpSemantics = webrtc.SDPSemanticsUnifiedPlanWithFallback
	case "plan-b":
		sdpSemantics = webrtc.SDPSemanticsPlanB
	}

	w := WebRTCTransportConfig{
		configuration: webrtc.Configuration{
			ICEServers:   iceServers,
			SDPSemantics: sdpSemantics,
		},
		setting: se,
		router:  c.Router,
	}

	if len(c.WebRTC.Candidates.NAT1To1IPs) > 0 {
		w.setting.SetNAT1To1IPs(c.WebRTC.Candidates.NAT1To1IPs, webrtc.ICECandidateTypeHost)
	}

	return w
}

// NewSFU creates a new sfu instance
func NewSFU(c Config) *SFU {
	// Init random seed
	rand.Seed(time.Now().UnixNano())
	// Init ballast
	ballast := make([]byte, c.SFU.Ballast*1024*1024)
	// Init buffer factory
	bufferFactory = buffer.NewBufferFactory()
	// Init packet factory
	packetFactory = &sync.Pool{
		New: func() interface{} {
			return make([]byte, 1460)
		},
	}

	w := NewWebRTCTransportConfig(c)

	s := &SFU{
		webrtc:   w,
		sessions: make(map[string]*Session),
	}

	runtime.KeepAlive(ballast)
	return s
}

// NewSession creates a new session instance
func (s *SFU) newSession(id string) *Session {
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
func (s *SFU) getSession(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

func (s *SFU) GetSession(sid string) (*Session, WebRTCTransportConfig) {
	session := s.getSession(sid)
	if session == nil {
		session = s.newSession(sid)
	}
	return session, s.webrtc
}
