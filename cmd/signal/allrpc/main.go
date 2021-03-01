package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/pion/ion-sfu/cmd/signal/allrpc/server"
	log "github.com/pion/ion-sfu/pkg/logger"
	"github.com/pion/ion-sfu/pkg/sfu"
	"github.com/spf13/viper"
)

var (
	file                       string
	cert                       string
	key                        string
	gaddr, jaddr, paddr, maddr string
	verbosityLevel             int
)

// Config defines parameters for configuring the sfu instance
type Config struct {
	sfu.Config `mapstructure:",squash"`
	LogConfig  log.GlobalConfig `mapstructure:"log"`
}

var (
	conf = Config{}
)

func showHelp() {
	fmt.Printf("Usage:%s {params}\n", os.Args[0])
	fmt.Println("      -c {config file}")
	fmt.Println("      -cert {cert file used by jsonrpc}")
	fmt.Println("      -key {key file used by jsonrpc}")
	fmt.Println("      -gaddr {grpc listen addr}")
	fmt.Println("      -jaddr {jsonrpc listen addr}")
	fmt.Println("      -paddr {pprof listen addr}")
	fmt.Println("             {grpc and jsonrpc addrs should be set at least one}")
	fmt.Println("      -maddr {metrics listen addr}")
	fmt.Println("      -h (show help info)")
	fmt.Println("      -v (verbosity level, default 0)")
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

	if conf.LogConfig.V < 0 {
		fmt.Printf("Logger V-Level cannot be less than 0\n")
		return false
	}

	fmt.Printf("config %s load ok!\n", file)
	return true
}

func parse() bool {
	flag.StringVar(&file, "c", "config.toml", "config file")
	flag.StringVar(&cert, "cert", "", "cert file")
	flag.StringVar(&key, "key", "", "key file")
	flag.StringVar(&jaddr, "jaddr", "", "jsonrpc listening address")
	flag.StringVar(&gaddr, "gaddr", "", "grpc listening address")
	flag.StringVar(&paddr, "paddr", "", "pprof listening address")
	flag.StringVar(&maddr, "maddr", "", "metrics listening address")
	flag.IntVar(&verbosityLevel, "v", -1, "verbosity level, higher value - more logs")
	help := flag.Bool("h", false, "help info")
	flag.Parse()

	// at least set one
	if gaddr == "" && jaddr == "" {
		return false
	}

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

	conf.Config.Logger = log.New()
	node := server.New(conf.Config)

	if gaddr != "" {
		go node.ServeGRPC(gaddr)
	}

	if jaddr != "" {
		go node.ServeJSONRPC(jaddr, cert, key)
	}

	if paddr != "" {
		go node.ServePProf(paddr)
	}

	if maddr != "" {
		go node.ServeMetrics(maddr)
	}

	select {}
}
