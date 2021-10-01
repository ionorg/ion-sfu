package buffer

import (
	"io"
	"sync/atomic"
)

type RTCPReader interface {
	Write(p []byte) (n int, err error)
	OnClose(fn func())
	Close() error
	OnPacket(f func([]byte))
	Read(_ []byte) (n int, err error)
}

type reader struct {
	ssrc     uint32
	closed   atomicBool
	onPacket atomic.Value //func([]byte)
	onClose  func()
}

func NewRTCPReader(ssrc uint32) *reader {
	return &reader{ssrc: ssrc}
}

func (r *reader) Write(p []byte) (n int, err error) {
	if r.closed.get() {
		err = io.EOF
		return
	}
	if f, ok := r.onPacket.Load().(func([]byte)); ok {
		f(p)
	}
	return
}

func (r *reader) OnClose(fn func()) {
	r.onClose = fn
}

func (r *reader) Close() error {
	r.closed.set(true)
	r.onClose()
	return nil
}

func (r *reader) OnPacket(f func([]byte)) {
	r.onPacket.Store(f)
}

func (r *reader) Read(_ []byte) (n int, err error) { return }
