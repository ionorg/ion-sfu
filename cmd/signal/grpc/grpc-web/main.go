package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/improbable-eng/grpc-web/go/grpcweb"
	pb "github.com/pion/ion-sfu/cmd/signal/grpc/proto"
	"github.com/pion/ion-sfu/cmd/signal/grpc/server"
	sfu "github.com/pion/ion-sfu/pkg"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
)

var (
	enableTLS             = pflag.Bool("enable_tls", false, "Use TLS - required for HTTP2.")
	configFile            = pflag.String("c", "config.toml", "Config file.")
	tlsCertFilePath       = pflag.String("tls_cert_file", "./certs/localhost.crt", "Path to the CRT/PEM file.")
	tlsKeyFilePath        = pflag.String("tls_key_file", "./certs/localhost.key", "Path to the private key file.")
	flagAllowAllOrigins   = pflag.Bool("allow_all_origins", true, "allow requests from any origin.")
	flagAllowedOrigins    = pflag.StringSlice("allowed_origins", nil, "comma-separated list of origin URLs which are allowed to make cross-origin requests.")
	flagAllowedHeaders    = pflag.StringSlice("allowed_headers", []string{}, "comma-separated list of headers which are allowed to propagate to the gRPC backend.")
	useWebsockets         = pflag.Bool("use_websockets", true, "whether to use beta websocket transport layer")
	websocketPingInterval = pflag.Duration("websocket_ping_interval", 0, "whether to use websocket keepalive pinging. Only used when using websockets. Configured interval must be >= 1s.")
)

type allowedOrigins struct {
	origins map[string]struct{}
}

func (a *allowedOrigins) IsAllowed(origin string) bool {
	_, ok := a.origins[origin]
	return ok
}
func makeAllowedOrigins(origins []string) *allowedOrigins {
	o := map[string]struct{}{}
	for _, allowedOrigin := range origins {
		o[allowedOrigin] = struct{}{}
	}
	return &allowedOrigins{
		origins: o,
	}
}

func makeHTTPOriginFunc(allowedOrigins *allowedOrigins) func(origin string) bool {
	if *flagAllowAllOrigins {
		return func(origin string) bool {
			return true
		}
	}
	return allowedOrigins.IsAllowed
}

func makeWebsocketOriginFunc(allowedOrigins *allowedOrigins) func(req *http.Request) bool {
	if *flagAllowAllOrigins {
		return func(req *http.Request) bool {
			return true
		}
	} else {
		return func(req *http.Request) bool {
			origin, err := grpcweb.WebsocketRequestOrigin(req)
			if err != nil {
				grpclog.Warning(err)
				return false
			}
			return allowedOrigins.IsAllowed(origin)
		}
	}
}

type grpcConfig struct {
	Port string `mapstructure:"port"`
}

// Config defines parameters for configuring the sfu instance
type Config struct {
	sfu.Config `mapstructure:",squash"`
	GRPC       grpcConfig `mapstructure:"grpc"`
}

var (
	conf = Config{}
	file string
	addr string
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
	_, err := os.Stat(*configFile)
	if err != nil {
		return false
	}

	viper.SetConfigFile(*configFile)
	viper.SetConfigType("toml")

	err = viper.ReadInConfig()
	if err != nil {
		fmt.Printf("config file %s read failed. %v\n", *configFile, err)
		return false
	}
	err = viper.GetViper().Unmarshal(&conf)
	if err != nil {
		fmt.Printf("sfu config file %s loaded failed. %v\n", *configFile, err)
		return false
	}

	if len(conf.WebRTC.ICEPortRange) > 2 {
		fmt.Printf("config file %s loaded failed. range port must be [min,max]\n", *configFile)
		return false
	}

	if len(conf.WebRTC.ICEPortRange) != 0 && conf.WebRTC.ICEPortRange[1]-conf.WebRTC.ICEPortRange[0] < portRangeLimit {
		fmt.Printf("config file %s loaded failed. range port must be [min, max] and max - min >= %d\n", *configFile, portRangeLimit)
		return false
	}

	fmt.Printf("config %s load ok!\n", *configFile)
	return true
}

func main() {

	pflag.Parse()

	if !load() {
		os.Exit(-1)
	}

	port := 9090
	if *enableTLS {
		port = 9091
	}

	if *flagAllowAllOrigins && len(*flagAllowedOrigins) != 0 {
		logrus.Fatal("Ambiguous --allow_all_origins and --allow_origins configuration. Either set --allow_all_origins=true OR specify one or more origins to whitelist with --allow_origins, not both.")
	}

	grpcServer := grpc.NewServer()

	inst := server.GRPCSignal{SFU: sfu.NewSFU(conf.Config)}
	pb.RegisterSFUService(grpcServer, &pb.SFUService{
		Signal: inst.Signal,
	})

	grpclog.SetLogger(log.New(os.Stdout, "ION-SFU: ", log.LstdFlags))
	grpclog.Info("--- Starting SFU Node (grpc-web) ---")

	allowedOrigins := makeAllowedOrigins(*flagAllowedOrigins)

	options := []grpcweb.Option{
		grpcweb.WithCorsForRegisteredEndpointsOnly(false),
		grpcweb.WithOriginFunc(makeHTTPOriginFunc(allowedOrigins)),
	}

	if *useWebsockets {
		grpclog.Println("Using websockets")
		options = append(
			options,
			grpcweb.WithWebsockets(true),
			grpcweb.WithWebsocketOriginFunc(makeWebsocketOriginFunc(allowedOrigins)),
		)

		if *websocketPingInterval >= time.Second {
			logrus.Infof("websocket keepalive pinging enabled, the timeout interval is %s", websocketPingInterval.String())
		}
		options = append(
			options,
			grpcweb.WithWebsocketPingInterval(*websocketPingInterval),
		)
	}

	if len(*flagAllowedHeaders) > 0 {
		options = append(
			options,
			grpcweb.WithAllowedRequestHeaders(*flagAllowedHeaders),
		)
	}

	wrappedServer := grpcweb.WrapServer(grpcServer, options...)
	handler := func(resp http.ResponseWriter, req *http.Request) {
		wrappedServer.ServeHTTP(resp, req)
	}

	httpServer := http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: http.HandlerFunc(handler),
	}

	grpclog.Printf("Starting server. http port: %d, with TLS: %v", port, *enableTLS)

	if *enableTLS {
		if err := httpServer.ListenAndServeTLS(*tlsCertFilePath, *tlsKeyFilePath); err != nil {
			grpclog.Fatalf("failed starting http2 server: %v", err)
		}
	} else {
		if err := httpServer.ListenAndServe(); err != nil {
			grpclog.Fatalf("failed starting http server: %v", err)
		}
	}
}
