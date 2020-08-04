package sfu

import (
	"net"
	"sync"

	"github.com/lucsky/cuid"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/relay"
	"github.com/pion/ion-sfu/pkg/relay/mux"
	"github.com/pion/udp"
)

const (
	receiveMTU = 1500
)

// RelayTransportConfig defines configuration parameters
// for an RelayTransport
type RelayTransportConfig struct {
	Addr string
}

// RelayTransport ..
type RelayTransport struct {
	id           string
	mu           sync.RWMutex
	rtpSession   *relay.SessionRTP
	rtcpSession  *relay.SessionRTCP
	rtpEndpoint  *mux.Endpoint
	rtcpEndpoint *mux.Endpoint
	conn         net.Conn
	mux          *mux.Mux
	stop         bool
	routers      map[uint32]*Router
}

// NewRelayTransport create a RelayTransport by net.Conn
func NewRelayTransport(conn net.Conn) *RelayTransport {
	m := mux.NewMux(mux.Config{
		Conn:       conn,
		BufferSize: receiveMTU,
	})

	t := &RelayTransport{
		id:           cuid.New(),
		conn:         conn,
		routers:      make(map[uint32]*Router),
		mux:          m,
		rtpEndpoint:  m.NewEndpoint(mux.MatchRTP),
		rtcpEndpoint: m.NewEndpoint(mux.MatchRTCP),
	}

	var err error
	t.rtpSession, err = relay.NewSessionRTP(t.rtpEndpoint)
	if err != nil {
		log.Errorf("relay.NewSessionRTP => %s", err.Error())
		return nil
	}

	t.rtcpSession, err = relay.NewSessionRTCP(t.rtcpEndpoint)
	if err != nil {
		log.Errorf("relay.NewSessionRTCP => %s", err.Error())
		return nil
	}

	go t.acceptStreams()

	return t
}

// NewSender for peer
func (t *RelayTransport) NewSender(track Track) (Sender, error) {
	return NewRelaySender(track, t), nil
}

// AddSub adds peer as a sub
func (t *RelayTransport) AddSub(transport Transport) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, router := range t.routers {
		sender, err := transport.NewSender(router.Track())
		if err != nil {
			log.Errorf("Error subscribing transport %s to router %v", transport.ID(), router)
		}
		router.AddSender(transport.ID(), sender)
	}
}

// ID return id
func (t *RelayTransport) ID() string {
	return t.id
}

// Routers returns routers for this peer
func (t *RelayTransport) Routers() map[uint32]*Router {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.routers
}

// Close release all
func (t *RelayTransport) Close() {
	if t.stop {
		return
	}
	log.Infof("RelayTransport.Close()")
	t.stop = true
	t.rtpSession.Close()
	t.rtcpSession.Close()
	t.rtpEndpoint.Close()
	t.rtcpEndpoint.Close()
	t.mux.Close()
	t.conn.Close()
}

func (t *RelayTransport) getRTPSession() *relay.SessionRTP {
	return t.rtpSession
}

// func (t *RelayTransport) getRTCPSession() *relay.SessionRTCP {
// 	return t.rtcpSession
// }

// ReceiveRTP receive rtp
func (t *RelayTransport) acceptStreams() {
	for {
		if t.stop {
			break
		}
		// stream, ssrc, err := t.rtpSession.AcceptStream()
		// if err == relay.ErrSessionRTPClosed {
		// 	t.Close()
		// 	return
		// } else if err != nil {
		// 	log.Warnf("Failed to accept stream %v ", err)
		// 	continue
		// }

		// go func() {
		// 	for {
		// 		if t.stop {
		// 			return
		// 		}
		// 		rtpBuf := make([]byte, receiveMTU)
		// 		_, pkt, err := stream.ReadRTP(rtpBuf)
		// 		if err != nil {
		// 			log.Warnf("Failed to read rtp %v %d ", err, ssrc)
		// 			//for non-blocking ReadRTP()
		// 			t.rtpCh <- nil
		// 			continue
		// 		}

		// 		log.Debugf("RelayTransport.acceptStreams pkt=%v", pkt)

		// 		t.rtpCh <- pkt
		// 	}
		// }()
	}
}

func (t *RelayTransport) stats() string {
	return ""
}

const (
	maxRtpConnSize = 1024
)

// NewRelayClient returns a new RTP client connection
func NewRelayClient(addr string) net.Conn {
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

// RelayServer represents an RTP UDP RelayServer
type RelayServer struct {
	listener net.Listener
	ch       chan *RelayTransport
	mu       sync.RWMutex
	stop     bool
}

// NewRelayServer creates a new RTP Server for lisening to RTP over UDP
func NewRelayServer(port int) *RelayServer {
	log.Infof("relay.Serve port=%d ", port)

	listener, err := udp.Listen("udp", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		log.Errorf("failed to listen %v", err)
		return nil
	}

	r := &RelayServer{
		listener: listener,
		ch:       make(chan *RelayTransport, maxRtpConnSize),
	}

	go r.accept()

	return r
}

func (r *RelayServer) accept() {
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

		r.ch <- NewRelayTransport(conn)
	}
}

// Accept new RelayTransports
func (r *RelayServer) Accept() *RelayTransport {
	t := <-r.ch
	return t
}

// Close close listener and break loop
func (r *RelayServer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stop {
		return
	}

	r.stop = true
	r.listener.Close()
}
