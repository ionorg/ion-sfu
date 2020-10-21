// Package cmd contains an entrypoint for running an ion-sfu instance.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/gorilla/websocket"
	"github.com/sourcegraph/jsonrpc2"
	websocketjsonrpc2 "github.com/sourcegraph/jsonrpc2/websocket"
	"github.com/spf13/viper"

	grpcServer "github.com/pion/ion-sfu/cmd/signal/grpc/server"
	jsonrpcServer "github.com/pion/ion-sfu/cmd/signal/json-rpc/server"
	"google.golang.org/grpc"

	pb "github.com/pion/ion-sfu/cmd/signal/grpc/proto"
	sfu "github.com/pion/ion-sfu/pkg"
	"github.com/pion/ion-sfu/pkg/log"
)

var (
	conf     = sfu.Config{}
	file     string
	cert     string
	key      string
	jrpcAddr string
	grpcAddr string
)

const (
	portRangeLimit = 100
)

func showHelp() {
	fmt.Printf("Usage:%s {params}\n", os.Args[0])
	fmt.Println("      -c {config file}")
	fmt.Println("      -cert {cert file}")
	fmt.Println("      -key {key file}")
	fmt.Println("      -a {listen grpcAddr}")
	fmt.Println("      -a {listen jrpcAddr}")
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

	if len(conf.WebRTC.ICEPortRange) != 0 && conf.WebRTC.ICEPortRange[1]-conf.WebRTC.ICEPortRange[0] < portRangeLimit {
		fmt.Printf("config file %s loaded failed. range port must be [min, max] and max - min >= %d\n", file, portRangeLimit)
		return false
	}

	fmt.Printf("config %s load ok!\n", file)
	return true
}

func parse() bool {
	flag.StringVar(&file, "c", "config.toml", "config file")
	flag.StringVar(&cert, "cert", "", "cert file")
	flag.StringVar(&key, "key", "", "key file")
	flag.StringVar(&jrpcAddr, "j", ":7000", "address to use for JSON-RPC")
	flag.StringVar(&grpcAddr, "g", ":50051", "address to use for GRPC")
	help := flag.Bool("h", false, "help info")
	flag.Parse()
	if !load() {
		return false
	}

	if *help {
		return false
	}
	return true
}

func main() {
	if !parse() {
		showHelp()
		os.Exit(-1)
	}
	log.Init(conf.Log.Level, conf.Log.Fix)

	log.Infof("--- Starting SFU Node ---")

	lis, err := net.Listen("tcp", grpcAddr)

	if err != nil {
		log.Panicf("failed to listen: %v", err)
	}

	s := sfu.NewSFU(conf)
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	_s := grpc.NewServer()
	inst := grpcServer.GRPCSignal{SFU: s}

	pb.RegisterSFUService(_s, &pb.SFUService{
		Signal: inst.Signal,
	})

	http.Handle("/ws", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			panic(err)
		}
		defer c.Close()

		p := jsonrpcServer.NewJSONSignal(sfu.NewPeer(s))
		defer p.Close()

		jc := jsonrpc2.NewConn(r.Context(), websocketjsonrpc2.NewObjectStream(c), p)
		<-jc.DisconnectNotify()
	}))

	go func() {
		if key != "" && cert != "" {
			log.Infof("Listening at https://[%s]", jrpcAddr)
			err = http.ListenAndServeTLS(jrpcAddr, cert, key, nil)
		} else {
			log.Infof("Listening at http://[%s]", jrpcAddr)
			err = http.ListenAndServe(jrpcAddr, nil)
		}
		if err != nil {
			panic(err)
		}
	}()

	if err := _s.Serve(lis); err != nil {
		log.Panicf("failed to serve: %v", err)
	}
}
