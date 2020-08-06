package sfu

import (
	"net"
	"sync"

	"github.com/lucsky/cuid"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/relay"
	"github.com/pion/ion-sfu/pkg/relay/mux"
	"github.com/pion/webrtc/v3"
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
	me           *RelayMediaEngine
	mu           sync.RWMutex
	rtpSession   *relay.SessionRTP
	rtcpSession  *relay.SessionRTCP
	rtpEndpoint  *mux.Endpoint
	rtcpEndpoint *mux.Endpoint
	mux          *mux.Mux
	stop         bool
	session      *Session
	routers      map[uint32]*Router
}

// NewRelayTransport create a RelayTransport by net.Conn
func NewRelayTransport(session *Session, conn net.Conn) (*RelayTransport, error) {
	m := mux.NewMux(mux.Config{
		Conn:       conn,
		BufferSize: receiveMTU,
	})

	t := &RelayTransport{
		id:           cuid.New(),
		me:           NewRelayMediaEngine(),
		session:      session,
		routers:      make(map[uint32]*Router),
		mux:          m,
		rtpEndpoint:  m.NewEndpoint(mux.MatchRTP),
		rtcpEndpoint: m.NewEndpoint(mux.MatchRTCP),
	}

	session.AddTransport(t)

	var err error
	t.rtpSession, err = relay.NewSessionRTP(t.rtpEndpoint)
	if err != nil {
		log.Errorf("relay.NewSessionRTP => %s", err.Error())
		return nil, err
	}

	t.rtcpSession, err = relay.NewSessionRTCP(t.rtcpEndpoint)
	if err != nil {
		log.Errorf("relay.NewSessionRTCP => %s", err.Error())
		return nil, err
	}

	// go t.acceptStreams()

	return t, nil
}

// NewSender for peer
func (t *RelayTransport) NewSender(track Track) (Sender, error) {
	return NewRelaySender(track, t), nil
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

// GetRouter returns router with ssrc
func (t *RelayTransport) GetRouter(ssrc uint32) *Router {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.routers[ssrc]
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
}

func (t *RelayTransport) getRTPSession() *relay.SessionRTP {
	return t.rtpSession
}

// func (t *RelayTransport) getRTCPSession() *relay.SessionRTCP {
// 	return t.rtcpSession
// }

// ReceiveRTP receive rtp
func (t *RelayTransport) acceptRTP() {
	for {
		if t.stop {
			break
		}

		stream, err := t.rtpSession.AcceptStream()
		if err == relay.ErrSessionRTPClosed {
			t.Close()
			return
		} else if err != nil {
			log.Warnf("Failed to accept stream %v ", err)
			continue
		}

		codec, err := t.me.getCodec(stream.PayloadType())
		if err != nil {
			log.Errorf("Relay codec not supported: %s", err)
			continue
		}

		track, err := webrtc.NewTrack(stream.PayloadType(), stream.ID(), cuid.New(), "relay", codec)
		if err != nil {
			log.Errorf("Relay error creating track: %s", err)
			continue
		}

		recv := NewRelayReceiver(track, stream)
		router := NewRouter(t.id, recv)
		log.Debugf("Created router %s %d", t.id, recv.Track().SSRC())

		t.session.AddRouter(router)

		t.mu.Lock()
		t.routers[recv.Track().SSRC()] = router
		t.mu.Unlock()
	}
}

func (t *RelayTransport) stats() string {
	return ""
}
