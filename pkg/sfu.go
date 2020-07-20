package sfu

import (
	"fmt"
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
	peers       map[string]*Peer
	peerLock    sync.RWMutex
	routers     map[uint32]*Router
	routersLock sync.RWMutex
}

// NewSFU creates a new sfu instance
func NewSFU(config Config) *SFU {
	s := &SFU{
		peers:   make(map[string]*Peer),
		routers: make(map[uint32]*Router),
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

// Connect a webrtc stream
func (s *SFU) Connect(offer webrtc.SessionDescription) (*Peer, *webrtc.SessionDescription, error) {
	peer, err := NewPeer(offer)
	peer.onRecv(func(recv Receiver) {
		s.routersLock.Lock()
		s.routers[recv.Track().SSRC()] = NewRouter(recv)
		s.routersLock.Unlock()
	})

	answer, err := peer.Answer(offer)
	if err != nil {
		log.Errorf("Publish error: pc.CreateAnswer answer=%v err=%v", answer, err)
		return nil, nil, err
	}

	log.Debugf("Publish: answer => %v", answer)
	return peer, &answer, nil
}

// SubTracks to a peer
func (s *SFU) SubTracks(pid string, ssrcs []uint32) {
	peer := s.peers[pid]

	for _, ssrc := range ssrcs {
		router := s.routers[ssrc]
		send, err := peer.NewSender(router.pub.Track())

		if err != nil {
			log.Errorf("Error creating send track")
			continue
		}

		router.AddSub(pid, send)
	}
}

func (s *SFU) stats() {
	t := time.NewTicker(statCycle)
	for range t.C {
		info := "\n----------------rtc-----------------\n"

		// info += "peer: " + string(s.id) + "\n"

		s.routersLock.Lock()
		router := s.routers
		if len(router) < 6 {
			for ssrc := range router {
				info += fmt.Sprintf("router: %d\n", ssrc)
			}
			info += "\n"
		} else {
			info += fmt.Sprintf("routers: %d\n\n", len(router))
		}

		s.routersLock.Unlock()
		log.Infof(info)
	}
}
