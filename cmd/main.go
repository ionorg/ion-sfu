// Package cmd contains an entrypoint for running an ion-sfu instance.
package main

import (
	"net/http"

	conf "github.com/pion/ion-sfu/pkg/conf"
	"github.com/pion/ion-sfu/pkg/log"
	sfu "github.com/pion/ion-sfu/pkg/node"
	"github.com/pion/ion-sfu/pkg/rtc"
	"github.com/pion/ion-sfu/pkg/rtc/plugins"
	"github.com/pion/webrtc/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func init() {
	var icePortStart, icePortEnd uint16

	if len(conf.WebRTC.ICEPortRange) == 2 {
		icePortStart = conf.WebRTC.ICEPortRange[0]
		icePortEnd = conf.WebRTC.ICEPortRange[1]
	}

	log.Init(conf.Log.Level)
	var iceServers []webrtc.ICEServer
	for _, iceServer := range conf.WebRTC.ICEServers {
		s := webrtc.ICEServer{
			URLs:       iceServer.URLs,
			Username:   iceServer.Username,
			Credential: iceServer.Credential,
		}
		iceServers = append(iceServers, s)
	}
	if err := rtc.InitIce(iceServers, icePortStart, icePortEnd); err != nil {
		panic(err)
	}

	if err := rtc.InitRTP(conf.Rtp.Port, conf.Rtp.KcpKey, conf.Rtp.KcpSalt); err != nil {
		panic(err)
	}

	if conf.Plugins.Metrics != "" {
		go func() {
			log.Infof("Serving metrics at %s/metrics", conf.Plugins.Metrics)
			http.Handle("/metrics", promhttp.Handler())
			err := http.ListenAndServe(conf.Plugins.Metrics, nil)
			if err != nil {
				panic(err)
			}
		}()
	}

	pluginConfig := plugins.Config{
		On: conf.Plugins.On,
		JitterBuffer: plugins.JitterBufferConfig{
			On:            conf.Plugins.JitterBuffer.On,
			TCCOn:         conf.Plugins.JitterBuffer.TCCOn,
			REMBCycle:     conf.Plugins.JitterBuffer.REMBCycle,
			PLICycle:      conf.Plugins.JitterBuffer.PLICycle,
			MaxBandwidth:  conf.Plugins.JitterBuffer.MaxBandwidth,
			MaxBufferTime: conf.Plugins.JitterBuffer.MaxBufferTime,
		},
		RTPForwarder: plugins.RTPForwarderConfig{
			On:      conf.Plugins.RTPForwarder.On,
			Addr:    conf.Plugins.RTPForwarder.Addr,
			KcpKey:  conf.Plugins.RTPForwarder.KcpKey,
			KcpSalt: conf.Plugins.RTPForwarder.KcpSalt,
		},
	}

	if err := rtc.CheckPlugins(pluginConfig); err != nil {
		panic(err)
	}
	rtc.InitPlugins(pluginConfig)
	rtc.InitRouter(*conf.Router)
}

func main() {
	log.Infof("--- Starting SFU Node ---")
	sfu.Init(conf.GRPC.Port)
	select {}
}
