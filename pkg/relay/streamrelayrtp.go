package relay

import (
	"fmt"
	"sync"

	"github.com/pion/rtp"
	"github.com/pion/transport/packetio"
)

// Limit the buffer size to 1MB
const rtpBufferSize = 1000 * 1000

// RTPPacket represents a relay rtp packet
type RTPPacket struct {
	Relay *Packet
	RTP   *rtp.Packet
}

// ReadStreamRelayRTP handles decryption for a single RTP SSRC
type ReadStreamRelayRTP struct {
	mu sync.Mutex

	isInited bool
	isClosed chan bool

	session   *SessionRTP
	sessionID uint32
	ssrc      uint32

	buffer *packetio.Buffer
}

// Used by getOrCreateReadStream
func newReadStreamRelayRTP() readStream {
	return &ReadStreamRelayRTP{}
}

func (r *ReadStreamRelayRTP) init(child streamSession, sessionID uint32, ssrc uint32) error {
	sessionRTP, ok := child.(*SessionRTP)

	r.mu.Lock()
	defer r.mu.Unlock()

	if !ok {
		return fmt.Errorf("ReadStreamRelayRTP init failed type assertion")
	} else if r.isInited {
		return fmt.Errorf("ReadStreamRelayRTP has already been inited")
	}

	r.session = sessionRTP
	r.sessionID = sessionID
	r.ssrc = ssrc
	r.isInited = true
	r.isClosed = make(chan bool)

	// Create a buffer with a 1MB limit
	r.buffer = packetio.NewBuffer()
	r.buffer.SetLimitSize(rtpBufferSize)

	return nil
}

func (r *ReadStreamRelayRTP) write(buf []byte) (n int, err error) {
	n, err = r.buffer.Write(buf)

	if err == packetio.ErrFull {
		// Silently drop data when the buffer is full.
		return len(buf), nil
	}

	return n, err
}

// Read reads and decrypts full RTP packet from the nextConn
func (r *ReadStreamRelayRTP) Read(buf []byte) (int, error) {
	return r.buffer.Read(buf)
}

// ReadRelayRTP reads and decrypts full RTP packet and its header from the nextConn
func (r *ReadStreamRelayRTP) ReadRelayRTP(buf []byte) (int, *RTPPacket, error) {
	n, err := r.Read(buf)
	if err != nil {
		return 0, nil, err
	}

	// Unmarshal relay packet
	relay := &Packet{}
	if err := relay.Unmarshal(buf[:n]); err != nil {
		return 0, nil, err
	}

	rtp := &rtp.Packet{}
	err = rtp.Unmarshal(relay.Payload)
	if err != nil {
		return 0, nil, err
	}

	return n, &RTPPacket{
		Relay: relay,
		RTP:   rtp,
	}, nil
}

// Close removes the ReadStream from the session and cleans up any associated state
func (r *ReadStreamRelayRTP) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.isInited {
		return fmt.Errorf("ReadStreamRelayRTP has not been inited")
	}

	select {
	case <-r.isClosed:
		return fmt.Errorf("ReadStreamRelayRTP is already closed")
	default:
		err := r.buffer.Close()
		if err != nil {
			return err
		}

		r.session.removeReadStream(r.ssrc)
		return nil
	}

}

// GetSSRC returns the SSRC we are demuxing for
func (r *ReadStreamRelayRTP) GetSSRC() uint32 {
	return r.ssrc
}

// GetSessionID returns the session ID we are demuxing for
func (r *ReadStreamRelayRTP) GetSessionID() uint32 {
	return r.ssrc
}

// WriteStreamRelayRTP is stream for a single Session that is used to encrypt RTP
type WriteStreamRelayRTP struct {
	session *SessionRTP
}

// WriteRelayRTP writes a relay packet to the connection
func (w *WriteStreamRelayRTP) WriteRelayRTP(rh *Header, header *rtp.Header, payload []byte) (int, error) {
	return w.session.writeRelayRTP(rh, header, payload)
}
