// Package cmd contains an entrypoint for running an ion-sfu instance.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/pion/ion-sfu/pkg/middlewares/datachannel"

	log "github.com/pion/ion-sfu/pkg/logger"
	"github.com/pion/ion-sfu/pkg/sfu"
	"github.com/spf13/viper"

	"github.com/pion/ion-sfu/cmd/signal/grpc/server"
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
	verbosityLevel int
	logger         = log.New()
)

const (
	portRangeLimit = 100
)

func showHelp() {
	fmt.Printf("Usage:%s {params}\n", os.Args[0])
	fmt.Println("      -c {config file}")
	fmt.Println("      -a {listen addr}")
	fmt.Println("      -h (show help info)")
	fmt.Println("      -v {0-10} (verbosity level, default 0)")
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

	if conf.LogConfig.V < 0 {
		logger.Error(nil, "Logger V-Level cannot be less than 0")
		return false
	}

	logger.Info("Config file loaded", "file", file)
	return true

}

func parse() bool {
	flag.StringVar(&file, "c", "config.toml", "config file")
	flag.StringVar(&addr, "a", ":9090", "address to use")
	flag.IntVar(&verbosityLevel, "v", -1, "verbosity level, higher value - more logs")
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

	// Check that the -v is not set (default -1)
	if verbosityLevel < 0 {
		verbosityLevel = conf.LogConfig.V
	}

	log.SetGlobalOptions(log.GlobalConfig{V: verbosityLevel})
	logger := log.New()

	logger.Info("--- Starting SFU Node ---")
	options := server.DefaultWrapperedServerOptions()
	options.EnableTLS = false
	options.Addr = addr
	options.AllowAllOrigins = true
	options.UseWebSocket = true

	sfu.Logger = logger
	nsfu := sfu.NewSFU(conf.Config)
	dc := nsfu.NewDatachannel(sfu.APIChannelLabel)
	dc.Use(datachannel.SubscriberAPI)
	s := server.NewWrapperedGRPCWebServer(options, nsfu)
	if err := s.Serve(); err != nil {
		logger.Error(err, "failed to serve")
		os.Exit(1)
	}
	select {}
}
