// Package cmd contains an entrypoint for running an ion-sfu instance.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"

	"github.com/pion/ion-sfu/pkg/log"
	sfu "github.com/pion/ion-sfu/pkg/node"
	"github.com/pion/webrtc/v2"
	"github.com/spf13/viper"
	"golang.org/x/net/websocket"
)

var (
	conf = sfu.Config{}
	file string
)

const (
	portRangeLimit = 100
)

func showHelp() {
	fmt.Printf("Usage:%s {params}\n", os.Args[0])
	fmt.Println("      -c {config file}")
	fmt.Println("      -h (show help info)")
}

func load() bool {
	_, err := os.Stat(file)
	if err != nil {
		return false
	}

	viper.SetConfigFile(file)
	viper.SetConfigType("toml")

	err = viper.ReadInConfig()
	if err != nil {
		fmt.Printf("config file %s read failed. %v\n", file, err)
		return false
	}
	err = viper.GetViper().Unmarshal(&conf)
	if err != nil {
		fmt.Printf("sfu config file %s loaded failed. %v\n", file, err)
		return false
	}

	if len(conf.WebRTC.ICEPortRange) > 2 {
		fmt.Printf("config file %s loaded failed. range port must be [min,max]\n", file)
		return false
	}

	if len(conf.WebRTC.ICEPortRange) != 0 && conf.WebRTC.ICEPortRange[1]-conf.WebRTC.ICEPortRange[0] <= portRangeLimit {
		fmt.Printf("config file %s loaded failed. range port must be [min, max] and max - min >= %d\n", file, portRangeLimit)
		return false
	}

	fmt.Printf("config %s load ok!\n", file)
	return true
}

func parse() bool {
	flag.StringVar(&file, "c", "config.toml", "config file")
	help := flag.Bool("h", false, "help info")
	flag.Parse()
	if !load() {
		return false
	}

	if *help {
		showHelp()
		return false
	}
	return true
}

// RPC defines the json-rpc
type RPC struct{}

// PublishRequest defines a json-rpc request to publish a stream
type PublishRequest struct {
	Rid   string
	Offer webrtc.SessionDescription
}

// PublishReply defines a json-rpc reply to a publish request
type PublishReply struct {
	Mid    string
	Answer webrtc.SessionDescription
}

// Publish a stream to the sfu
func (r *RPC) Publish(req *PublishRequest, resp *PublishReply) error {
	log.Infof("Got publish request: %v", req)
	mid, _, answer, err := sfu.Publish(req.Offer)
	if err != nil {
		return err
	}

	resp.Mid = mid
	resp.Answer = *answer

	return nil
}

// SubscribeRequest defines a json-rpc request to publish a stream
type SubscribeRequest struct {
	Mid   string
	Offer webrtc.SessionDescription
}

// SubscribeReply defines a json-rpc reply to a publish request
type SubscribeReply struct {
	Mid    string
	Answer webrtc.SessionDescription
}

// Subscribe a stream to the sfu
func (r *RPC) Subscribe(req *SubscribeRequest, resp *SubscribeReply) error {
	log.Infof("Got subscribe request: %v", req)
	mid, _, answer, err := sfu.Subscribe(req.Mid, req.Offer)
	if err != nil {
		return err
	}

	resp.Mid = mid
	resp.Answer = *answer

	return nil
}

func main() {
	if !parse() {
		showHelp()
		os.Exit(-1)
	}

	sfu.Init(conf)
	log.Infof("--- Starting SFU Node ---")
	err := rpc.Register(new(RPC))
	if err != nil {
		panic(err)
	}

	http.Handle("/ws", websocket.Handler(serve))

	log.Infof("Listening at %s", "localhost:7000")
	err = http.ListenAndServe("localhost:7000", nil)
	if err != nil {
		panic(err)
	}
}

func serve(ws *websocket.Conn) {
	jsonrpc.ServeConn(ws)
}
