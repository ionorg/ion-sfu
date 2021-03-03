package server

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/improbable-eng/grpc-web/go/grpcweb"
	pb "github.com/pion/ion-sfu/cmd/signal/grpc/proto"
	"github.com/pion/ion-sfu/pkg/sfu"
	"github.com/soheilhy/cmux"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

type WrapperedServerOptions struct {
	Addr                  string
	EnableTLS             bool
	TLSAddr               string
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
		EnableTLS:             false,
		TLSAddr:               ":9091",
		Cert:                  "",
		Key:                   "",
		AllowAllOrigins:       false,
		AllowedHeaders:        &[]string{},
		AllowedOrigins:        &[]string{},
		UseWebSocket:          true,
		WebsocketPingInterval: time.Second * 30,
	}
}

type WrapperedGRPCWebServer struct {
	options WrapperedServerOptions
	sfu     *sfu.SFU
}

func NewWrapperedGRPCWebServer(options WrapperedServerOptions, sfu *sfu.SFU) *WrapperedGRPCWebServer {
	return &WrapperedGRPCWebServer{
		options: options,
		sfu:     sfu,
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
			sfu.Logger.Error(err, "Request error")
			return false
		}
		return allowedOrigins.IsAllowed(origin)
	}
}

func (s *WrapperedGRPCWebServer) Serve() error {
	addr := s.options.Addr
	if s.options.EnableTLS {
		addr = s.options.TLSAddr
	}
	if s.options.AllowAllOrigins && s.options.AllowedOrigins != nil && len(*s.options.AllowedOrigins) != 0 {
		sfu.Logger.Error(fmt.Errorf("Configuration error"), "Ambiguous --allow_all_origins and --allow_origins configuration. Either set --allow_all_origins=true OR specify one or more origins to whitelist with --allow_origins, not both.")
	}
	grpcServer := grpc.NewServer()
	pb.RegisterSFUServer(grpcServer, &SFUServer{SFU: s.sfu})

	allowedOrigins := makeAllowedOrigins(*s.options.AllowedOrigins)

	options := []grpcweb.Option{
		grpcweb.WithCorsForRegisteredEndpointsOnly(false),
		grpcweb.WithOriginFunc(s.makeHTTPOriginFunc(allowedOrigins)),
	}

	if s.options.UseWebSocket {
		sfu.Logger.V(0).Info("Using websockets")
		options = append(
			options,
			grpcweb.WithWebsockets(true),
			grpcweb.WithWebsocketOriginFunc(s.makeWebsocketOriginFunc(allowedOrigins)),
		)

		if s.options.WebsocketPingInterval >= time.Second {
			sfu.Logger.V(0).Info("websocket keepalive pinging enabled", "timeout_interval", s.options.WebsocketPingInterval.String())
		}
		options = append(
			options,
			grpcweb.WithWebsocketPingInterval(s.options.WebsocketPingInterval),
		)
	}

	if s.options.AllowedHeaders != nil && len(*s.options.AllowedHeaders) > 0 {
		options = append(
			options,
			grpcweb.WithAllowedRequestHeaders(*s.options.AllowedHeaders),
		)
	}

	wrappedServer := grpcweb.WrapServer(grpcServer, options...)
	handler := func(resp http.ResponseWriter, req *http.Request) {
		wrappedServer.ServeHTTP(resp, req)
	}

	httpServer := http.Server{
		Addr:    addr,
		Handler: http.HandlerFunc(handler),
	}

	var listener net.Listener

	if s.options.EnableTLS {
		cer, err := tls.LoadX509KeyPair(s.options.Cert, s.options.Key)
		if err != nil {
			sfu.Logger.Error(err, "failed to load x509 key pair")
			return err
		}
		config := &tls.Config{Certificates: []tls.Certificate{cer}}
		tls, err := tls.Listen("tcp", addr, config)
		if err != nil {
			sfu.Logger.Error(err, "failed to listen: tls")
			return err
		}
		listener = tls
	} else {
		tcp, err := net.Listen("tcp", addr)
		if err != nil {
			sfu.Logger.Error(err, "failed to listen: tcp")
			return err
		}
		listener = tcp
	}

	sfu.Logger.V(0).Info("Starting grpc/grpc-web server", "addr", addr, "tls_enabled", s.options.EnableTLS)

	m := cmux.New(listener)
	grpcListener := m.Match(cmux.HTTP2HeaderField("content-type", "application/grpc"))
	httpListener := m.Match(cmux.HTTP1Fast())
	g := new(errgroup.Group)
	g.Go(func() error { return grpcServer.Serve(grpcListener) })
	g.Go(func() error { return httpServer.Serve(httpListener) })
	g.Go(m.Serve)
	sfu.Logger.Error(g.Wait(), "Run server")
	return nil
}
