package relay

import (
	"net"
	"sync"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/relay/mux"
	"github.com/pion/udp"
)

const (
	maxConnSize = 1024
)

// NewClient returns a new relay client connection
func NewClient(addr string) net.Conn {
	srcAddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	dstAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Errorf("Error creating RelayTransport client err=%v", err)
		return nil
	}

	// Wait for client to connect
	conn, err := net.DialUDP("udp", srcAddr, dstAddr)
	if err != nil {
		log.Errorf("Error creating RelayTransport client err=%v", err)
		return nil
	}

	return conn
}

// ServerSessionMux represents a stream session demuxer
type ServerSessionMux struct {
	stop     bool
	ch       chan *ReadStreamRelay
	session  *SessionRelay
	endpoint *mux.Endpoint
}

// NewServerSessionMux demuxs connection based on session
func NewServerSessionMux(conn net.Conn) (*ServerSessionMux, error) {
	m := mux.NewMux(mux.Config{
		Conn:       conn,
		BufferSize: receiveMTU,
	})

	r := &ServerSessionMux{
		ch:       make(chan *ReadStreamRelay, maxConnSize),
		endpoint: m.NewEndpoint(mux.MatchAll),
	}

	var err error
	r.session, err = NewSessionRelay(r.endpoint)
	if err != nil {
		log.Errorf("NewSessionRelay => %s", err.Error())
		return nil, err
	}

	return r, nil
}

// AcceptTransport new ReadStreamRelays
func (r *ServerSessionMux) AcceptTransport() (*ReadStreamRelay, error) {
	return r.session.AcceptStream()
}

func (r *ServerSessionMux) close() {
	if r.stop {
		return
	}
	r.stop = true
	close(r.ch)
}

// Server represents an RTP UDP Server
type Server struct {
	listener net.Listener
	ch       chan *ServerSessionMux
	mu       sync.RWMutex
	stop     bool
}

// NewServer creates a new RTP Server for lisening to RTP over UDP
func NewServer(port int) *Server {
	log.Infof("Serve port=%d ", port)

	listener, err := udp.Listen("udp", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		log.Errorf("failed to listen %v", err)
		return nil
	}

	r := &Server{
		listener: listener,
		ch:       make(chan *ServerSessionMux, maxConnSize),
	}

	go r.accept()

	return r
}

func (r *Server) accept() {
	for {
		r.mu.RLock()
		if r.stop {
			return
		}
		r.mu.RUnlock()

		conn, err := r.listener.Accept()
		if err != nil {
			log.Errorf("failed to accept conn %v", err)
			continue
		}
		log.Infof("accept new rtp conn %s", conn.RemoteAddr().String())

		t, err := NewServerSessionMux(conn)
		if err != nil {
			log.Errorf("Error creating ServerSessionMux %s", err)
			continue
		}

		r.ch <- t
	}
}

// AcceptRelay new RelayTransports
func (r *Server) AcceptRelay() *ServerSessionMux {
	t := <-r.ch
	return t
}

// Close close listener and break loop
func (r *Server) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stop {
		return
	}

	r.stop = true
	r.listener.Close()
}
