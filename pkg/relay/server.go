package relay

import (
	"io"
	"net"
	"sync"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/udp"
)

const (
	maxConnSize = 1024
)

// ServerSession represents a stream session demuxer
type ServerSession struct {
	ch chan *SessionConn
}

// NewServerSession waits for the first relay packet to create a SessionConn
func NewServerSession(conn net.Conn) (*ServerSession, error) {
	s := &ServerSession{
		ch: make(chan *SessionConn, 1),
	}

	go s.start(conn)

	return s, nil
}

// Wait for first packet to read session id
func (s *ServerSession) start(conn net.Conn) {
	b := make([]byte, receiveMTU)
	i, err := conn.Read(b)
	if err != nil {
		if err != io.EOF {
			log.Errorf("s.Read => %s", err.Error())
		}
		return
	}
	if i == 0 {
		log.Warnf("s.Read = 0")
		return
	}

	// Unmarshal relay packet
	p := &Packet{}
	if err := p.Unmarshal(b[:i]); err != nil {
		log.Errorf("Error unmarshaling relay packet: %s", err)
		return
	}

	nextConn := &SessionConn{conn, p.SessionID}
	s.ch <- nextConn

	_, err = nextConn.Write(b[:i])
	if err != nil {
		log.Errorf("Error writing relay payload: %s", err)
		return
	}
}

// Accept new relay conns
func (s *ServerSession) Accept() *SessionConn {
	c := <-s.ch
	return c
}

// Server represents an RTP UDP Server
type Server struct {
	listener net.Listener
	ch       chan *SessionConn
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
		ch:       make(chan *SessionConn, maxConnSize),
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

		if err != nil && r.stop {
			return
		} else if err != nil {
			log.Errorf("failed to accept conn %v", err)
			continue
		}
		log.Infof("accept new rtp conn %s", conn.RemoteAddr().String())

		t, err := NewServerSession(conn)
		if err != nil {
			log.Errorf("Error creating ServerSession %s", err)
			continue
		}

		go func() {
			r.ch <- t.Accept()
		}()
	}
}

// AcceptSession new Session
func (r *Server) AcceptSession() *SessionConn {
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
