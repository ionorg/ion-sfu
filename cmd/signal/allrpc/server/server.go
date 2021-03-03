package server

import (
	"net"
	"net/http"

	"github.com/go-logr/logr"
	"github.com/pion/ion-sfu/pkg/middlewares/datachannel"

	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	pb "github.com/pion/ion-sfu/cmd/signal/grpc/proto"
	grpcServer "github.com/pion/ion-sfu/cmd/signal/grpc/server"
	jsonrpcServer "github.com/pion/ion-sfu/cmd/signal/json-rpc/server"
	"github.com/pion/ion-sfu/pkg/sfu"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	// pprof
	_ "net/http/pprof"

	"github.com/gorilla/websocket"
	"github.com/sourcegraph/jsonrpc2"
	websocketjsonrpc2 "github.com/sourcegraph/jsonrpc2/websocket"
)

type Server struct {
	sfu    *sfu.SFU
	logger logr.Logger
}

// New create a server which support grpc/jsonrpc
func New(c sfu.Config, logger logr.Logger) *Server { // Register default middlewares
	s := sfu.NewSFU(c)
	sfu.Logger = logger
	dc := s.NewDatachannel(sfu.APIChannelLabel)
	dc.Use(datachannel.SubscriberAPI)
	return &Server{
		sfu:    s,
		logger: logger,
	}
}

// ServeGRPC serve grpc
func (s *Server) ServeGRPC(gaddr string) error {
	l, err := net.Listen("tcp", gaddr)
	if err != nil {
		return err
	}

	gs := grpc.NewServer(
		grpc.StreamInterceptor(grpc_prometheus.StreamServerInterceptor),
	)
	pb.RegisterSFUServer(gs, grpcServer.NewServer(s.sfu))
	s.logger.Info("GRPC Listening", "addr", gaddr)

	if err := gs.Serve(l); err != nil {
		s.logger.Error(err, "grpc server error")
		return err
	}
	return nil
}

// ServeJSONRPC serve jsonrpc
func (s *Server) ServeJSONRPC(jaddr, cert, key string) error {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	http.Handle("/ws", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			panic(err)
		}
		defer c.Close()

		p := jsonrpcServer.NewJSONSignal(sfu.NewPeer(s.sfu), s.logger)
		defer p.Close()

		jc := jsonrpc2.NewConn(r.Context(), websocketjsonrpc2.NewObjectStream(c), p)
		<-jc.DisconnectNotify()
	}))

	var err error
	if key != "" && cert != "" {
		s.logger.Info("JsonRPC Listening", "addr", "https://"+jaddr)
		err = http.ListenAndServeTLS(jaddr, cert, key, nil)
	} else {
		s.logger.Info("JsonRPC Listening", "addr", "http://"+jaddr)
		err = http.ListenAndServe(jaddr, nil)
	}
	if err != nil {
		s.logger.Error(err, "JsonRPC starting error")
	}
	return err
}

// ServePProf
func (s *Server) ServePProf(paddr string) {
	s.logger.Info("PProf Listening", "addr", paddr)
	http.ListenAndServe(paddr, nil)
}

// ServeMetrics
func (s *Server) ServeMetrics(maddr string) {
	// start metrics server
	m := http.NewServeMux()
	m.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{
		Handler: m,
	}

	metricsLis, err := net.Listen("tcp", maddr)
	if err != nil {
		s.logger.Error(err, "Cannot bind to metrics endpoint", "addr", maddr)
	}
	s.logger.Info("Metrics Listening", "addr", "http://"+maddr)

	err = srv.Serve(metricsLis)
	if err != nil {
		s.logger.Error(err, "Metrics server stopped with error")
	}
}
