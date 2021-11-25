package server

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/improbable-eng/grpc-web/go/grpcweb"
	log "github.com/pion/ion-log"
	"github.com/pion/ion-sfu/pkg/sfu"
	rtc "github.com/pion/ion/proto/rtc"
	"github.com/soheilhy/cmux"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type WrapperedServerOptions struct {
	Addr                  string
	Cert                  string
	Key                   string
	AllowAllOrigins       bool
	AllowedOrigins        *[]string
	AllowedHeaders        *[]string
	UseWebSocket          bool
	WebsocketPingInterval time.Duration
}

func DefaultWrapperedServerOptions() WrapperedServerOptions {
	return WrapperedServerOptions{
		Addr:                  ":9090",
		Cert:                  "",
		Key:                   "",
		AllowAllOrigins:       true,
		AllowedHeaders:        &[]string{},
		AllowedOrigins:        &[]string{},
		UseWebSocket:          true,
		WebsocketPingInterval: 0,
	}
}

func NewWrapperedServerOptions(addr, cert, key string, websocket bool) WrapperedServerOptions {
	return WrapperedServerOptions{
		Addr:                  addr,
		Cert:                  cert,
		Key:                   key,
		AllowAllOrigins:       true,
		AllowedHeaders:        &[]string{},
		AllowedOrigins:        &[]string{},
		UseWebSocket:          true,
		WebsocketPingInterval: 0,
	}
}

type WrapperedGRPCWebServer struct {
	options    WrapperedServerOptions
	GRPCServer *grpc.Server
}

func NewWrapperedGRPCWebServer(options WrapperedServerOptions, s *grpc.Server) *WrapperedGRPCWebServer {
	return &WrapperedGRPCWebServer{
		options:    options,
		GRPCServer: s,
	}
}

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

func (s *WrapperedGRPCWebServer) makeHTTPOriginFunc(allowedOrigins *allowedOrigins) func(origin string) bool {
	if s.options.AllowAllOrigins {
		return func(origin string) bool {
			return true
		}
	}
	return allowedOrigins.IsAllowed
}

func (s *WrapperedGRPCWebServer) makeWebsocketOriginFunc(allowedOrigins *allowedOrigins) func(req *http.Request) bool {
	if s.options.AllowAllOrigins {
		return func(req *http.Request) bool {
			return true
		}
	}
	return func(req *http.Request) bool {
		origin, err := grpcweb.WebsocketRequestOrigin(req)
		if err != nil {
			log.Warnf("%v", err)
			return false
		}
		return allowedOrigins.IsAllowed(origin)
	}
}

func (s *WrapperedGRPCWebServer) Serve() error {
	addr := s.options.Addr

	if s.options.AllowAllOrigins && s.options.AllowedOrigins != nil && len(*s.options.AllowedOrigins) != 0 {
		log.Errorf("Ambiguous --allow_all_origins and --allow_origins configuration. Either set --allow_all_origins=true OR specify one or more origins to whitelist with --allow_origins, not both.")
	}

	allowedOrigins := makeAllowedOrigins(*s.options.AllowedOrigins)

	options := []grpcweb.Option{
		grpcweb.WithCorsForRegisteredEndpointsOnly(false),
		grpcweb.WithOriginFunc(s.makeHTTPOriginFunc(allowedOrigins)),
	}

	if s.options.UseWebSocket {
		log.Infof("Using websockets")
		options = append(
			options,
			grpcweb.WithWebsockets(true),
			grpcweb.WithWebsocketOriginFunc(s.makeWebsocketOriginFunc(allowedOrigins)),
		)

		if s.options.WebsocketPingInterval >= time.Second {
			log.Infof("websocket keepalive pinging enabled, the timeout interval is %s", s.options.WebsocketPingInterval.String())
			options = append(
				options,
				grpcweb.WithWebsocketPingInterval(s.options.WebsocketPingInterval),
			)
		}
	}

	if s.options.AllowedHeaders != nil && len(*s.options.AllowedHeaders) > 0 {
		options = append(
			options,
			grpcweb.WithAllowedRequestHeaders(*s.options.AllowedHeaders),
		)
	}

	wrappedServer := grpcweb.WrapServer(s.GRPCServer, options...)
	handler := func(resp http.ResponseWriter, req *http.Request) {
		wrappedServer.ServeHTTP(resp, req)
	}

	httpServer := http.Server{
		Addr:    addr,
		Handler: http.HandlerFunc(handler),
	}

	var listener net.Listener

	enableTLS := s.options.Cert != "" && s.options.Key != ""

	if enableTLS {
		cer, err := tls.LoadX509KeyPair(s.options.Cert, s.options.Key)
		if err != nil {
			log.Panicf("failed to load x509 key pair: %v", err)
			return err
		}
		config := &tls.Config{Certificates: []tls.Certificate{cer}}
		tls, err := tls.Listen("tcp", addr, config)
		if err != nil {
			log.Panicf("failed to listen: tls %v", err)
			return err
		}
		listener = tls
	} else {
		tcp, err := net.Listen("tcp", addr)
		if err != nil {
			log.Panicf("failed to listen: tcp %v", err)
			return err
		}
		listener = tcp
	}

	log.Infof("Starting gRPC/gRPC-Web combo server, bind: %s, with TLS: %v", addr, enableTLS)

	m := cmux.New(listener)
	grpcListener := m.Match(cmux.HTTP2())
	httpListener := m.Match(cmux.HTTP1Fast())
	g := new(errgroup.Group)
	g.Go(func() error { return s.GRPCServer.Serve(grpcListener) })
	g.Go(func() error { return httpServer.Serve(httpListener) })
	g.Go(m.Serve)
	log.Infof("Run server: %v", g.Wait())
	return nil
}

func WrapperedGRPCWebServe(sfu *sfu.SFU, addr, cert, key string) error {
	grpcServer := grpc.NewServer(
		grpc.StreamInterceptor(grpc_prometheus.StreamServerInterceptor),
	)

	rtc.RegisterRTCServer(grpcServer, NewSFUServer(sfu))
	grpc_health_v1.RegisterHealthServer(grpcServer, health.NewServer())
	grpc_prometheus.Register(grpcServer)

	log.Infof("wrappered grpc listening %v", addr)
	options := NewWrapperedServerOptions(addr, cert, key, true)
	wrapperedSrv := NewWrapperedGRPCWebServer(options, grpcServer)
	if err := wrapperedSrv.Serve(); err != nil {
		log.Errorf("wrappered grpc listening error: %v", err)
		return err
	}
	return nil
}
