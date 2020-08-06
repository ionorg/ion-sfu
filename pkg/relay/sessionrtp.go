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
	writeStream *WriteStreamRTP
}

// NewSessionRTP creates a RTP session using conn as the underlying transport.
func NewSessionRTP(conn net.Conn) (*SessionRTP, error) {
	s := &SessionRTP{
		session: session{
			nextConn:    conn,
			readStreams: map[uint32]readStream{},
			newStream:   make(chan readStream),
			started:     make(chan interface{}),
			closed:      make(chan interface{}),
		},
	}
	s.writeStream = &WriteStreamRTP{s}

	err := s.session.start(s)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// OpenWriteStream returns the global write stream for the Session
func (s *SessionRTP) OpenWriteStream() (*WriteStreamRTP, error) {
	return s.writeStream, nil
}

// OpenReadStream opens a read stream for the given SSRC, it can be used
// if you want a certain SSRC, but don't want to wait for AcceptStream
func (s *SessionRTP) OpenReadStream(SSRC uint32) (*ReadStreamRTP, error) {
	r, _ := s.session.getOrCreateReadStream(SSRC, s, newReadStreamRTP)

	if readStream, ok := r.(*ReadStreamRTP); ok {
		return readStream, nil
	}

	return nil, fmt.Errorf("failed to open ReadStreamSRCTP, type assertion failed")
}

// AcceptStream returns a stream to handle RTCP for a single SSRC
func (s *SessionRTP) AcceptStream() (*ReadStreamRTP, error) {
	stream, ok := <-s.newStream
	if !ok {
		return nil, ErrSessionRTPClosed
	}

	readStream, ok := stream.(*ReadStreamRTP)
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
	rtp := &rtp.Packet{}
	err := rtp.Unmarshal(b)
	if err != nil {
		return 0, nil
	}

	return s.writeRTP(&rtp.Header, rtp.Payload)
}

func (s *SessionRTP) writeRTP(header *rtp.Header, payload []byte) (int, error) {
	if _, ok := <-s.session.started; ok {
		return 0, fmt.Errorf("started channel used incorrectly, should only be closed")
	}

	rtp := rtp.Packet{Header: *header, Payload: payload}
	bin, err := rtp.Marshal()
	if err != nil {
		return 0, err
	}

	return s.session.nextConn.Write(bin)
}

func (s *SessionRTP) handle(buf []byte) error {
	// Unmarshal rtp header for relay payload
	h := &rtp.Header{}
	if err := h.Unmarshal(buf); err != nil {
		return err
	}

	r, isNew := s.session.getOrCreateReadStream(h.SSRC, s, newReadStreamRTP)
	if r == nil {
		return nil // Session has been closed
	}

	readStream, ok := r.(*ReadStreamRTP)
	if !ok {
		return fmt.Errorf("failed to get/create ReadStreamSRTP")
	}

	if isNew {
		readStream.payloadType = h.PayloadType
		s.session.newStream <- readStream // Notify AcceptStream
	}

	_, err := readStream.write(buf)
	if err != nil {
		return err
	}

	return nil
}
