package server

import (
	"net"
	"net/http"

	log "github.com/pion/ion-log"
	grpcServer "github.com/pion/ion-sfu/cmd/signal/grpc/server"
	jsonrpcServer "github.com/pion/ion-sfu/cmd/signal/json-rpc/server"
	"github.com/pion/ion-sfu/pkg/sfu"

	// pprof
	_ "net/http/pprof"

	"github.com/gorilla/websocket"
	"github.com/sourcegraph/jsonrpc2"
	websocketjsonrpc2 "github.com/sourcegraph/jsonrpc2/websocket"
)

type Server struct {
	sfu *sfu.SFU
}

// New create a server which support grpc/jsonrpc
func New(c sfu.Config) *Server {
	return &Server{
		sfu: sfu.NewSFU(c),
	}
}

// ServeGRPC serve grpc
func (s *Server) ServeGRPC(gaddr string) error {
	l, err := net.Listen("tcp", gaddr)
	if err != nil {
		return err
	}

	gs := grpcServer.NewServer(s.sfu)
	log.Infof("GRPC Listening at %s", gaddr)
	if err := gs.Serve(l); err != nil {
		log.Errorf("err=%v", err)
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

		p := jsonrpcServer.NewJSONSignal(sfu.NewPeer(s.sfu))
		defer p.Close()

		jc := jsonrpc2.NewConn(r.Context(), websocketjsonrpc2.NewObjectStream(c), p)
		<-jc.DisconnectNotify()
	}))

	var err error
	if key != "" && cert != "" {
		log.Infof("JsonRPC Listening at https://[%s]", jaddr)
		err = http.ListenAndServeTLS(jaddr, cert, key, nil)
	} else {
		log.Infof("JsonRPC Listening at http://[%s]", jaddr)
		err = http.ListenAndServe(jaddr, nil)
	}
	if err != nil {
		log.Errorf("err=%v", err)
	}
	return err
}

// ServePProf
func (s *Server) ServePProf(paddr string) {
	log.Infof("PProf Listening at http://[%s]", paddr)
	http.ListenAndServe(paddr, nil)
}
