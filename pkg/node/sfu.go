package sfu

import (
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/rtc"
	"github.com/pion/ion-sfu/pkg/rtc/plugins"
	"github.com/pion/webrtc/v2"
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
	Router  rtc.RouterConfig `mapstructure:"router"`
	Plugins plugins.Config   `mapstructure:"plugins"`
	WebRTC  WebRTCConfig     `mapstructure:"webrtc"`
	Rtp     rtc.RTPConfig    `mapstructure:"rtp"`
	Log     log.Config       `mapstructure:"log"`
}

var (
	// only support unified plan
	cfg = webrtc.Configuration{
		SDPSemantics: webrtc.SDPSemanticsUnifiedPlanWithFallback,
	}

	setting webrtc.SettingEngine
)

// Init initialized the sfu
func Init(config Config) {
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

	if err := rtc.InitRTP(config.Rtp); err != nil {
		panic(err)
	}

	if err := rtc.CheckPlugins(config.Plugins); err != nil {
		panic(err)
	}
	rtc.InitPlugins(config.Plugins)
	rtc.InitRouter(config.Router)
}
