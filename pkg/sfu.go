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
	rooms    map[string]*Room
	roomLock sync.RWMutex
}

// NewSFU creates a new sfu instance
func NewSFU(config Config) *SFU {
	s := &SFU{
		rooms: make(map[string]*Room),
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

// CreateRoom creates a new room instance
func (s *SFU) CreateRoom(id string) *Room {
	room := NewRoom(id)
	room.OnClose(func() {
		s.roomLock.Lock()
		delete(s.rooms, id)
		s.roomLock.Unlock()
	})

	s.roomLock.Lock()
	s.rooms[id] = room
	s.roomLock.Unlock()
	return room
}

// GetRoom by id
func (s *SFU) GetRoom(id string) *Room {
	s.roomLock.RLock()
	defer s.roomLock.RUnlock()
	return s.rooms[id]
}

func (s *SFU) stats() {
	t := time.NewTicker(statCycle)
	for range t.C {
		info := "\n----------------stats-----------------\n"

		s.roomLock.RLock()
		if len(s.rooms) == 0 {
			s.roomLock.RUnlock()
			continue
		}

		for _, room := range s.rooms {
			info += room.stats()
		}
		s.roomLock.RUnlock()
		log.Infof(info)
	}
}
