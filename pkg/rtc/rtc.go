package rtc

import (
	"fmt"
	"sync"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/rtc/plugins"
	"github.com/pion/ion-sfu/pkg/rtc/rtpengine"
	"github.com/pion/ion-sfu/pkg/rtc/transport"
)

const (
	statCycle = 3 * time.Second
)

var (
	routers    = make(map[string]*Router)
	routerLock sync.RWMutex

	pluginsConfig plugins.Config
	routerConfig  RouterConfig
	stop          bool
)

// RTPConfig defines parameters for the rtp engine
type RTPConfig struct {
	Port    int    `mapstructure:"port"`
	KcpKey  string `mapstructure:"kcpkey"`
	KcpSalt string `mapstructure:"kcpsalt"`
}

// InitIce ice urls
// func InitIce(iceServers []webrtc.ICEServer, icePortStart, icePortEnd uint16) error {
// 	//init ice urls and ICE settings
// 	return transport.InitWebRTC(iceServers, icePortStart, icePortEnd)
// }

func InitRouter(config RouterConfig) {
	routerConfig = config
}

// InitPlugins plugins config
func InitPlugins(config plugins.Config) {
	pluginsConfig = config
	log.Infof("InitPlugins pluginsConfig=%+v", pluginsConfig)
}

// CheckPlugins plugins config
func CheckPlugins(config plugins.Config) error {
	return plugins.CheckPlugins(config)
}

// InitRTP rtp port
func InitRTP(config RTPConfig) error {
	// show stat about all routers
	go check()

	var connCh chan *transport.RTPTransport
	var err error
	// accept relay rtptransport
	if config.KcpKey != "" && config.KcpSalt != "" {
		connCh, err = rtpengine.ServeWithKCP(config.Port, config.KcpKey, config.KcpSalt)
	} else {
		connCh, err = rtpengine.Serve(config.Port)
	}
	if err != nil {
		log.Errorf("rtc.InitRPC err=%v", err)
		return err
	}
	go func() {
		for {
			if stop {
				return
			}
			for rtpTransport := range connCh {
				go func(rtpTransport *transport.RTPTransport) {
					id := <-rtpTransport.IDChan

					if id == "" {
						log.Errorf("invalid id from incoming rtp transport")
						return
					}

					log.Infof("accept new rtp id=%s conn=%s", id, rtpTransport.RemoteAddr().String())
					if router := AddRouter(id); router != nil {
						router.AddPub(rtpTransport)
					}
				}(rtpTransport)
			}
		}
	}()
	return nil
}

func GetOrNewRouter(id string) *Router {
	log.Infof("rtc.GetOrNewRouter id=%s", id)
	router := GetRouter(id)
	if router == nil {
		return AddRouter(id)
	}
	return router
}

// GetRouter get router from map
func GetRouter(id string) *Router {
	log.Infof("rtc.GetRouter id=%s", id)
	routerLock.RLock()
	defer routerLock.RUnlock()
	return routers[id]
}

// AddRouter add a new router
func AddRouter(id string) *Router {
	log.Infof("rtc.AddRouter id=%s", id)
	routerLock.Lock()
	defer routerLock.Unlock()
	router := NewRouter(id)
	router.OnClose(func() {
		delRouter(id)
	})
	if err := router.InitPlugins(pluginsConfig); err != nil {
		log.Errorf("rtc.AddRouter InitPlugins err=%v", err)
		return nil
	}
	routers[id] = router
	return routers[id]
}

// delRouter delete pub
func delRouter(id string) {
	log.Infof("delRouter id=%s", id)
	routerLock.Lock()
	defer routerLock.Unlock()
	delete(routers, id)
}

// check show all Routers' stat
func check() {
	t := time.NewTicker(statCycle)
	for range t.C {
		info := "\n----------------rtc-----------------\n"
		print := false
		routerLock.Lock()
		if len(routers) > 0 {
			print = true
		}

		for id, router := range routers {
			info += "pub: " + string(id) + "\n"
			subs := router.GetSubs()
			if len(subs) < 6 {
				for id := range subs {
					info += fmt.Sprintf("sub: %s\n", id)
				}
				info += "\n"
			} else {
				info += fmt.Sprintf("subs: %d\n\n", len(subs))
			}
		}
		routerLock.Unlock()
		if print {
			log.Infof(info)
		}
	}
}
