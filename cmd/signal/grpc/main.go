// Package cmd contains an entrypoint for running an ion-sfu instance.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"

	"github.com/pion/ion-sfu/cmd/signal/grpc/server"
	"github.com/pion/ion-sfu/pkg/middlewares/datachannel"

	log "github.com/pion/ion-sfu/pkg/logger"
	"github.com/pion/ion-sfu/pkg/sfu"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/viper"
)

type grpcConfig struct {
	Port string `mapstructure:"port"`
}

// Config defines parameters for configuring the sfu instance
type Config struct {
	sfu.Config `mapstructure:",squash"`
	GRPC       grpcConfig       `mapstructure:"grpc"`
	LogConfig  log.GlobalConfig `mapstructure:"log"`
}

var (
	conf           = Config{}
	file           string
	addr           string
	metricsAddr    string
	verbosityLevel int
	paddr          string

	enableTLS bool
	cert      string
	key       string

	logger = log.New()
)

const (
	portRangeLimit = 100
)

func showHelp() {
	fmt.Printf("Usage:%s {params}\n", os.Args[0])
	fmt.Println("      -c {config file}")
	fmt.Println("      -a {listen addr}")
	fmt.Println("      -tls (enable tls)")
	fmt.Println("      -cert {cert file}")
	fmt.Println("      -key {key file}")
	fmt.Println("      -h (show help info)")
	fmt.Println("      -v {0-10} (verbosity level, default 0)")
	fmt.Println("      -paddr {pprof listen addr}")

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
		logger.Error(err, "config file read failed", "file", file)
		return false
	}
	err = viper.GetViper().Unmarshal(&conf)
	if err != nil {
		logger.Error(err, "sfu config file loaded failed", "file", file)
		return false
	}

	if len(conf.WebRTC.ICEPortRange) > 2 {
		logger.Error(nil, "config file loaded failed. webrtc port must be [min,max]", "file", file)
		return false
	}

	if len(conf.WebRTC.ICEPortRange) != 0 && conf.WebRTC.ICEPortRange[1]-conf.WebRTC.ICEPortRange[0] < portRangeLimit {
		logger.Error(nil, "config file loaded failed. webrtc port must be [min, max] and max - min >= portRangeLimit", "file", file, "portRangeLimit", portRangeLimit)
		return false
	}

	if len(conf.Turn.PortRange) > 2 {
		logger.Error(nil, "config file loaded failed. turn port must be [min,max]", "file", file)
		return false
	}

	logger.V(0).Info("Config file loaded", "file", file)
	return true
}

func parse() bool {
	flag.StringVar(&file, "c", "config.toml", "config file")
	flag.StringVar(&addr, "a", ":50051", "address to use")
	flag.BoolVar(&enableTLS, "tls", false, "enable tls")
	flag.StringVar(&cert, "cert", "", "cert file")
	flag.StringVar(&key, "key", "", "key file")
	flag.StringVar(&metricsAddr, "m", ":8100", "merics to use")
	flag.IntVar(&verbosityLevel, "v", -1, "verbosity level, higher value - more logs")
	flag.StringVar(&paddr, "paddr", "", "pprof listening address")
	help := flag.Bool("h", false, "help info")
	flag.Parse()

	if paddr == "" {
		paddr = getEnv("paddr")
	}

	if !load() {
		return false
	}

	if *help {
		return false
	}
	return true
}

func getEnv(key string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}

	return ""
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
		logger.Error(err, "cannot bind to metrics endpoint", "addr", addr)
		os.Exit(1)
	}
	logger.Info("Metrics Listening starter", "addr", addr)

	err = srv.Serve(metricsLis)
	if err != nil {
		logger.Error(err, "debug server stopped. got err: %s")
	}
}

func main() {
	if !parse() {
		showHelp()
		os.Exit(-1)
	}

	// Check that the -v is not set (default -1)
	if verbosityLevel < 0 {
		verbosityLevel = conf.LogConfig.V
	}

	log.SetGlobalOptions(log.GlobalConfig{V: verbosityLevel})
	logger := log.New()

	logger.Info("--- Starting SFU Node ---")

	if paddr != "" {
		go func() {
			logger.Info("PProf Listening", "addr", paddr)
			_ = http.ListenAndServe(paddr, http.DefaultServeMux)
		}()
	}
	go startMetrics(metricsAddr)

	// SFU instance needs to be created with logr implementation
	sfu.Logger = logger

	nsfu := sfu.NewSFU(conf.Config)
	dc := nsfu.NewDatachannel(sfu.APIChannelLabel)
	dc.Use(datachannel.SubscriberAPI)

	err := server.WrapperedGRPCWebServe(nsfu, addr, cert, key)
	if err != nil {
		logger.Error(err, "failed to serve SFU")
		os.Exit(1)
	}
}
