package relay

import (
	"errors"
	"fmt"
	"net"

	"github.com/pion/rtp"
)

var (
	// ErrSessionRTPClosed is returned when a RTP session has been closed
	ErrSessionRTPClosed = errors.New("SessionRTP has been closed")
)

// SessionRTP represents an rtp session
type SessionRTP struct {
	session
	writeStream *WriteStreamRelayRTP
}

// NewSessionRelayRTP creates a RTP session using conn as the underlying transport.
func NewSessionRelayRTP(conn net.Conn) (*SessionRTP, error) {
	s := &SessionRTP{
		session: session{
			nextConn:    conn,
			readStreams: map[uint32]readStream{},
			newStream:   make(chan readStream),
			started:     make(chan interface{}),
			closed:      make(chan interface{}),
		},
	}
	s.writeStream = &WriteStreamRelayRTP{s}

	err := s.session.start(s)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// OpenWriteStream returns the global write stream for the Session
func (s *SessionRTP) OpenWriteStream() (*WriteStreamRelayRTP, error) {
	return s.writeStream, nil
}

// OpenReadStream opens a read stream for the given SSRC, it can be used
// if you want a certain SSRC, but don't want to wait for AcceptStream
func (s *SessionRTP) OpenReadStream(SessionID uint32, SSRC uint32) (*ReadStreamRelayRTP, error) {
	r, _ := s.session.getOrCreateReadStream(SessionID, SSRC, s, newReadStreamRelayRTP)

	if readStream, ok := r.(*ReadStreamRelayRTP); ok {
		return readStream, nil
	}

	return nil, fmt.Errorf("failed to open ReadStreamSRCTP, type assertion failed")
}

// AcceptStream returns a stream to handle RTCP for a single SSRC
func (s *SessionRTP) AcceptStream() (*ReadStreamRelayRTP, error) {
	stream, ok := <-s.newStream
	if !ok {
		return nil, ErrSessionRTPClosed
	}

	readStream, ok := stream.(*ReadStreamRelayRTP)
	if !ok {
		return nil, fmt.Errorf("newStream was found, but failed type assertion")
	}

	return readStream, nil
}

// Close ends the session
func (s *SessionRTP) Close() error {
	return s.session.close()
}

func (s *SessionRTP) write(b []byte) (int, error) {
	relay := &Packet{}
	err := relay.Unmarshal(b)
	if err != nil {
		return 0, nil
	}

	rtp := &rtp.Packet{}

	err = rtp.Unmarshal(relay.Payload)
	if err != nil {
		return 0, nil
	}

	return s.writeRelayRTP(&relay.Header, &rtp.Header, rtp.Payload)
}

func (s *SessionRTP) writeRelayRTP(rh *Header, header *rtp.Header, payload []byte) (int, error) {
	if _, ok := <-s.session.started; ok {
		return 0, fmt.Errorf("started channel used incorrectly, should only be closed")
	}

	rtp := rtp.Packet{Header: *header, Payload: payload}
	bin, err := rtp.Marshal()
	if err != nil {
		return 0, err
	}

	relay := Packet{Header: *rh, Payload: bin}
	bin, err = relay.Marshal()
	if err != nil {
		return 0, err
	}

	return s.session.nextConn.Write(bin)
}

func (s *SessionRTP) handle(buf []byte) error {
	// Unmarshal relay packet
	p := &Packet{}
	if err := p.Unmarshal(buf); err != nil {
		return err
	}

	// Unmarshal rtp header for relay payload
	h := &rtp.Header{}
	if err := h.Unmarshal(p.Payload); err != nil {
		return err
	}

	r, isNew := s.session.getOrCreateReadStream(p.SessionID, h.SSRC, s, newReadStreamRelayRTP)
	if r == nil {
		return nil // Session has been closed
	} else if isNew {
		s.session.newStream <- r // Notify AcceptStream
	}

	readStream, ok := r.(*ReadStreamRelayRTP)
	if !ok {
		return fmt.Errorf("failed to get/create ReadStreamSRTP")
	}

	_, err := readStream.write(buf)
	if err != nil {
		return err
	}

	return nil
}
