package buffer

import "io"

type RTCPReader struct {
	ssrc     uint32
	closed   bool
	onPacket func([]byte)
	onClose  func()
}

func NewRTCPReader(ssrc uint32) *RTCPReader {
	return &RTCPReader{ssrc: ssrc}
}

func (r *RTCPReader) Write(p []byte) (n int, err error) {
	if r.closed {
		err = io.EOF
		return
	}
	if r.onPacket != nil {
		r.onPacket(p)
	}
	return
}

func (r *RTCPReader) OnClose(fn func()) {
	r.onClose = fn
}

func (r *RTCPReader) Close() error {
	r.closed = true
	r.onClose()
	return nil
}

func (r *RTCPReader) OnPacket(f func([]byte)) {
	r.onPacket = f
}

func (r *RTCPReader) Read(_ []byte) (n int, err error) { return }
