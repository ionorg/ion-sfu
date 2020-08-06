package relay

import (
	"errors"
	"fmt"
	"net"

	"github.com/pion/rtp"
)

var (
	// ErrSessionRelayClosed is returned when a RTP session has been closed
	ErrSessionRelayClosed = errors.New("SessionRelay has been closed")
)

// SessionRelay represents an rtp session
type SessionRelay struct {
	session
	writeStream *WriteStreamRelay
}

// NewSessionRelay creates a RTP session using conn as the underlying transport.
func NewSessionRelay(conn net.Conn) (*SessionRelay, error) {
	s := &SessionRelay{
		session: session{
			nextConn:    conn,
			readStreams: map[uint32]readStream{},
			newStream:   make(chan readStream),
			started:     make(chan interface{}),
			closed:      make(chan interface{}),
		},
	}
	s.writeStream = &WriteStreamRelay{s}

	err := s.session.start(s)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// OpenWriteStream returns the global write stream for the Session
func (s *SessionRelay) OpenWriteStream() (*WriteStreamRelay, error) {
	return s.writeStream, nil
}

// OpenReadStream opens a read stream for the given SSRC, it can be used
// if you want a certain SSRC, but don't want to wait for AcceptStream
func (s *SessionRelay) OpenReadStream(SessionID uint32) (*ReadStreamRelay, error) {
	r, _ := s.session.getOrCreateReadStream(SessionID, s, newReadStreamRelay)

	if readStream, ok := r.(*ReadStreamRelay); ok {
		return readStream, nil
	}

	return nil, fmt.Errorf("failed to open ReadStreamSRCTP, type assertion failed")
}

// AcceptStream returns a stream to handle packets for a single session id
func (s *SessionRelay) AcceptStream() (*ReadStreamRelay, error) {
	stream, ok := <-s.newStream
	if !ok {
		return nil, ErrSessionRelayClosed
	}

	readStream, ok := stream.(*ReadStreamRelay)
	if !ok {
		return nil, fmt.Errorf("newStream was found, but failed type assertion")
	}

	return readStream, nil
}

// Close ends the session
func (s *SessionRelay) Close() error {
	return s.session.close()
}

func (s *SessionRelay) write(b []byte) (int, error) {
	pkt := &Packet{}
	err := pkt.Unmarshal(b)
	if err != nil {
		return 0, nil
	}

	return s.writeRelay(pkt)
}

func (s *SessionRelay) writeRelay(pkt *Packet) (int, error) {
	if _, ok := <-s.session.started; ok {
		return 0, fmt.Errorf("started channel used incorrectly, should only be closed")
	}

	bin, err := pkt.Marshal()
	if err != nil {
		return 0, err
	}

	return s.session.nextConn.Write(bin)
}

func (s *SessionRelay) handle(buf []byte) error {
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

	r, isNew := s.session.getOrCreateReadStream(p.SessionID, s, newReadStreamRelay)
	if r == nil {
		return nil // Session has been closed
	} else if isNew {
		s.session.newStream <- r // Notify AcceptStream
	}

	readStream, ok := r.(*ReadStreamRelay)
	if !ok {
		return fmt.Errorf("failed to get/create ReadStreamRelay")
	}

	_, err := readStream.Write(buf)
	if err != nil {
		return err
	}

	return nil
}
