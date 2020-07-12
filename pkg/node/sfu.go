package sfu

import (
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/rtc"
	"github.com/pion/ion-sfu/pkg/rtc/plugins"
	transport "github.com/pion/ion-sfu/pkg/rtc/transport"
)

// Config for base SFU
type Config struct {
	Router  rtc.RouterConfig       `mapstructure:"router"`
	Plugins plugins.Config         `mapstructure:"plugins"`
	WebRTC  transport.WebRTCConfig `mapstructure:"webrtc"`
	Rtp     rtc.RTPConfig          `mapstructure:"rtp"`
	Log     log.Config             `mapstructure:"log"`
}

// Init initialized the sfu
func Init(config Config) {
	log.Init(config.Log.Level)

	if err := transport.InitWebRTC(config.WebRTC); err != nil {
		panic(err)
	}

	if err := rtc.InitRTP(config.Rtp); err != nil {
		panic(err)
	}

	if err := rtc.CheckPlugins(config.Plugins); err != nil {
		panic(err)
	}
	rtc.InitPlugins(config.Plugins)
	rtc.InitRouter(config.Router)
}
