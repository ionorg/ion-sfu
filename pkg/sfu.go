package sfu

import (
	"math/rand"
	"net/url"
	"runtime"
	"sync"
	"time"

	"github.com/pion/sdp/v3"
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

	// Configure required extensions
	sdes, _ := url.Parse(sdp.SDESRTPStreamIDURI)
	sdesMid, _ := url.Parse(sdp.SDESMidURI)
	transportCCURL, _ := url.Parse(sdp.TransportCCURI)
	rtcpfb = append(rtcpfb, webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBTransportCC})
	rtcpfb = append(rtcpfb, webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBGoogREMB})
	se.AddSDPExtensions(webrtc.SDPSectionVideo,
		[]sdp.ExtMap{
			{
				URI: sdes,
			},
			{
				URI: sdesMid,
			},
			{
				URI: transportCCURL,
			},
		})
	se.AddSDPExtensions(webrtc.SDPSectionAudio,
		[]sdp.ExtMap{
			{
				URI: sdes,
			},
			{
				URI: sdesMid,
			},
			{
				URI: transportCCURL,
			},
		})

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

	sdpsemantics := webrtc.SDPSemanticsUnifiedPlan
	switch c.WebRTC.SDPSemantics {
	case "unified-plan-with-fallback":
		sdpsemantics = webrtc.SDPSemanticsUnifiedPlanWithFallback
	case "plan-b":
		sdpsemantics = webrtc.SDPSemanticsPlanB
	}

	w := WebRTCTransportConfig{
		configuration: webrtc.Configuration{
			ICEServers:   iceServers,
			SDPSemantics: sdpsemantics,
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
		defer s.mu.Unlock()
		delete(s.sessions, id)
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = session
	return session
}

// GetSession by id
func (s *SFU) getSession(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

// NewWebRTCTransport creates a new WebRTCTransport that is a member of a session
func (s *SFU) NewWebRTCTransport(sid string, me MediaEngine) (*WebRTCTransport, error) {
	session := s.getSession(sid)

	if session == nil {
		session = s.newSession(sid)
	}

	t, err := NewWebRTCTransport(session, me, s.webrtc)
	if err != nil {
		return nil, err
	}

	return t, nil
}
