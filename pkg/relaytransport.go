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
	id          string
	me          *RelayMediaEngine
	mu          sync.RWMutex
	rtpSession  *relay.SessionRTP
	rtpEndpoint *mux.Endpoint
	mux         *mux.Mux
	stop        bool
	session     *Session
	routers     map[uint32]*Router
}

// NewRelayTransport create a RelayTransport by net.Conn
func NewRelayTransport(session *Session, conn net.Conn) (*RelayTransport, error) {
	m := mux.NewMux(mux.Config{
		Conn:       conn,
		BufferSize: receiveMTU,
	})

	r := &RelayTransport{
		id:          cuid.New(),
		me:          NewRelayMediaEngine(),
		session:     session,
		routers:     make(map[uint32]*Router),
		mux:         m,
		rtpEndpoint: m.NewEndpoint(mux.MatchRTP),
	}

	session.AddTransport(r)

	var err error
	r.rtpSession, err = relay.NewSessionRTP(r.rtpEndpoint)
	if err != nil {
		log.Errorf("relay.NewSessionRTP => %s", err.Error())
		return nil, err
	}

	// Subscribe to existing transports
	for _, t := range session.Transports() {
		log.Infof("transport %s", t.ID())
		for _, router := range t.Routers() {
			sender, err := r.NewSender(router.Track())
			log.Infof("Init add router ssrc %d to %s", router.Track().SSRC(), r.id)
			if err != nil {
				log.Errorf("Error subscribing to router %v", router)
			}
			router.AddSender(r.id, sender)
		}
	}

	go r.acceptRTP()

	return r, nil
}

// NewSender for peer
func (r *RelayTransport) NewSender(track Track) (Sender, error) {
	stream, err := r.getRTPSession().OpenWriteStream()
	if err != nil {
		log.Errorf("Error opening write stream: %s", err)
		return nil, err
	}
	return NewRelaySender(track, stream), nil
}

// ID return id
func (r *RelayTransport) ID() string {
	return r.id
}

// Routers returns routers for this peer
func (r *RelayTransport) Routers() map[uint32]*Router {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.routers
}

// GetRouter returns router with ssrc
func (r *RelayTransport) GetRouter(ssrc uint32) *Router {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.routers[ssrc]
}

// Close release all
func (r *RelayTransport) Close() {
	if r.stop {
		return
	}
	log.Infof("RelayTransport.Close()")
	r.stop = true
	r.rtpSession.Close()
	r.rtpEndpoint.Close()
	r.mux.Close()
}

func (r *RelayTransport) getRTPSession() *relay.SessionRTP {
	return r.rtpSession
}

// ReceiveRTP receive rtp
func (r *RelayTransport) acceptRTP() {
	for {
		if r.stop {
			break
		}

		stream, err := r.rtpSession.AcceptStream()
		if err == relay.ErrSessionRTPClosed {
			r.Close()
			return
		} else if err != nil {
			log.Warnf("Failed to accept stream %v ", err)
			continue
		}

		codec, err := r.me.getCodec(stream.PayloadType())
		if err != nil {
			log.Errorf("Relay codec not supported: %s", err)
			continue
		}

		// TODO: Use originating track MediaStream ID
		track, err := webrtc.NewTrack(stream.PayloadType(), stream.ID(), cuid.New(), "relay", codec)
		if err != nil {
			log.Errorf("Relay error creating track: %s", err)
			continue
		}

		recv := NewRelayReceiver(track, stream)
		router := NewRouter(r.id, recv)
		log.Debugf("Created router %s %d", r.id, recv.Track().SSRC())

		r.session.AddRouter(router)

		r.mu.Lock()
		r.routers[recv.Track().SSRC()] = router
		r.mu.Unlock()
	}
}

func (r *RelayTransport) stats() string {
	return ""
}
