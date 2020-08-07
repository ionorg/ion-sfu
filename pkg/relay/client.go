package relay

import (
	"net"

	"github.com/pion/ion-sfu/pkg/log"
)

// NewClient returns a new relay client connection
func NewClient(sessionID uint32, addr string) *SessionConn {
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

	return &SessionConn{conn, sessionID}
}
