package relay

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/pion/transport/packetio"
)

// Limit the buffer size to 1MB
const relayBufferSize = 1000 * 1000

// ReadStreamRelay handles decryption for a single RTP SSRC
type ReadStreamRelay struct {
	mu sync.Mutex

	isInited bool
	isClosed chan bool

	session   *SessionRelay
	sessionID uint32

	buffer *packetio.Buffer
}

// Used by getOrCreateReadStream
func newReadStreamRelay() readStream {
	return &ReadStreamRelay{}
}

func (r *ReadStreamRelay) init(child streamSession, sessionID uint32) error {
	sessionRTP, ok := child.(*SessionRelay)

	r.mu.Lock()
	defer r.mu.Unlock()

	if !ok {
		return fmt.Errorf("ReadStreamRelay init failed type assertion")
	} else if r.isInited {
		return fmt.Errorf("ReadStreamRelay has already been inited")
	}

	r.session = sessionRTP
	r.sessionID = sessionID
	r.isInited = true
	r.isClosed = make(chan bool)

	// Create a buffer with a 1MB limit
	r.buffer = packetio.NewBuffer()
	r.buffer.SetLimitSize(relayBufferSize)

	return nil
}

func (r *ReadStreamRelay) Write(buf []byte) (n int, err error) {
	n, err = r.buffer.Write(buf)

	if err == packetio.ErrFull {
		// Silently drop data when the buffer is full.
		return len(buf), nil
	}

	return n, err
}

// Read reads and decrypts full RTP packet from the nextConn
func (r *ReadStreamRelay) Read(buf []byte) (int, error) {
	return r.buffer.Read(buf)
}

// LocalAddr is a stub
func (r *ReadStreamRelay) LocalAddr() net.Addr {
	return nil
}

// RemoteAddr is a stub
func (r *ReadStreamRelay) RemoteAddr() net.Addr {
	return nil
}

// SetDeadline is a stub
func (r *ReadStreamRelay) SetDeadline(t time.Time) error {
	return nil
}

// SetReadDeadline is a stub
func (r *ReadStreamRelay) SetReadDeadline(t time.Time) error {
	return nil
}

// SetWriteDeadline is a stub
func (r *ReadStreamRelay) SetWriteDeadline(t time.Time) error {
	return nil
}

// ReadRelay reads relay packets and its header from the nextConn
func (r *ReadStreamRelay) ReadRelay(buf []byte) (int, *Packet, error) {
	n, err := r.Read(buf)
	if err != nil {
		return 0, nil, err
	}

	// Unmarshal relay packet
	relay := &Packet{}
	if err := relay.Unmarshal(buf[:n]); err != nil {
		return 0, nil, err
	}

	return n, relay, nil
}

// Close removes the ReadStream from the session and cleans up any associated state
func (r *ReadStreamRelay) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.isInited {
		return fmt.Errorf("ReadStreamRelay has not been inited")
	}

	select {
	case <-r.isClosed:
		return fmt.Errorf("ReadStreamRelay is already closed")
	default:
		err := r.buffer.Close()
		if err != nil {
			return err
		}

		r.session.removeReadStream(r.sessionID)
		return nil
	}

}

// ID returns the session ID we are demuxing for
func (r *ReadStreamRelay) ID() uint32 {
	return r.sessionID
}

// WriteStreamRelay is stream for a single Session that is used to encrypt RTP
type WriteStreamRelay struct {
	session *SessionRelay
}

// WriteRelayRTP writes a relay packet to the connection
func (w *WriteStreamRelay) WriteRelayRTP(pkt *Packet) (int, error) {
	return w.session.writeRelay(pkt)
}
