// Package cmd contains an entrypoint for running an ion-sfu instance.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/go-logr/logr"
	"github.com/pion/ion-sfu/pkg/middlewares/datachannel"

	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	pb "github.com/pion/ion-sfu/cmd/signal/grpc/proto"
	"github.com/pion/ion-sfu/cmd/signal/grpc/server"
	log "github.com/pion/ion-sfu/pkg/logger"
	"github.com/pion/ion-sfu/pkg/sfu"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
)

type grpcConfig struct {
	Port string `mapstructure:"port"`
}

// Config defines parameters for configuring the sfu instance
type Config struct {
	sfu.Config `mapstructure:",squash"`
	GRPC       grpcConfig `mapstructure:"grpc"`
}

var (
	conf                                               = Config{}
	file                                               string
	addr                                               string
	metricsAddr                                        string
	defaultLogger, warnLogger, infoLogger, debugLogger logr.Logger
)

const (
	portRangeLimit = 100
)

func showHelp() {
	fmt.Printf("Usage:%s {params}\n", os.Args[0])
	fmt.Println("      -c {config file}")
	fmt.Println("      -a {listen addr}")
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
	flag.StringVar(&addr, "a", ":50051", "address to use")
	flag.StringVar(&metricsAddr, "m", ":8100", "merics to use")
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

func startMetrics(addr string) {
	// start metrics server
	m := http.NewServeMux()
	m.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{
		Handler: m,
	}

	metricsLis, err := net.Listen("tcp", addr)
	if err != nil {
		defaultLogger.Error(err, "cannot bind to metrics endpoint", "addr", addr)
		os.Exit(1)
	}
	infoLogger.Info("Metrics Listening starter", "addr", addr)

	err = srv.Serve(metricsLis)
	if err != nil {
		defaultLogger.Error(err, "debug server stopped. got err: %s")
	}
}

func main() {
	if !parse() {
		showHelp()
		os.Exit(-1)
	}

	// Creating loggers that implements logr interface, that used in sfu pkg
	// V-levels means different verbosity levels:
	// 0 - err, warn
	// 1 - info
	// 2 - debug
	// 3 - trace
	defaultLogger = log.New()
	infoLogger = defaultLogger.V(1)

	infoLogger.Info("--- Starting SFU Node ---")
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		defaultLogger.Error(err, "failed to listen")
		os.Exit(1)
	}

	s := grpc.NewServer(
		grpc.StreamInterceptor(grpc_prometheus.StreamServerInterceptor),
	)

	// SFU instance needs to be created with logr implementation
	conf.Config.Logger = defaultLogger

	nsfu := sfu.NewSFU(conf.Config)
	dc := nsfu.NewDatachannel(sfu.APIChannelLabel)
	dc.Use(datachannel.SubscriberAPI)

	pb.RegisterSFUServer(s, server.NewServer(nsfu))
	grpc_prometheus.Register(s)

	go startMetrics(metricsAddr)

	defaultLogger.Info("SFU Listening", "addr", addr)
	if err := s.Serve(lis); err != nil {
		defaultLogger.Error(err, "failed to serve SFU")
		os.Exit(1)
	}
}
