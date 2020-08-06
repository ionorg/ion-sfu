package mux

import (
	"net"
	"testing"
	"time"

	"github.com/pion/transport/test"
	"github.com/stretchr/testify/assert"
)

func TestStressDuplex(t *testing.T) {
	// Limit runtime in case of deadlocks
	lim := test.TimeOut(time.Second * 20)
	defer lim.Stop()

	// Check for leaking routines
	report := test.CheckRoutines(t)
	defer report()

	// Run the test
	stressDuplex(t)
}

func stressDuplex(t *testing.T) {
	ca, cb, stop := pipeMemory()

	defer func() {
		stop(t)
	}()

	opt := test.Options{
		MsgSize:  2048,
		MsgCount: 100,
	}

	err := test.StressDuplex(ca, cb, opt)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, ca.LocalAddr(), cb.RemoteAddr())
	assert.Equal(t, cb.LocalAddr(), ca.RemoteAddr())

	ca.Close()
	cb.Close()
}

func pipeMemory() (*Endpoint, net.Conn, func(*testing.T)) {
	// In memory pipe
	ca, cb := net.Pipe()

	matchAll := func([]byte) bool {
		return true
	}

	config := Config{
		Conn:       ca,
		BufferSize: 8192,
	}

	m := NewMux(config)
	e := m.NewEndpoint(matchAll)
	m.RemoveEndpoint(e)
	e = m.NewEndpoint(matchAll)

	stop := func(t *testing.T) {
		err := cb.Close()
		if err != nil {
			t.Fatal(err)
		}
		err = m.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	return e, cb, stop
}

func TestNoEndpoints(t *testing.T) {
	// In memory pipe
	ca, cb := net.Pipe()
	err := cb.Close()
	if err != nil {
		panic("Failed to close network pipe")
	}

	config := Config{
		Conn:       ca,
		BufferSize: 8192,
	}

	m := NewMux(config)
	err = m.dispatch(make([]byte, 1))
	if err != nil {
		t.Fatal(err)
	}
	err = m.Close()
	if err != nil {
		t.Fatalf("Failed to close empty mux")
	}
	err = ca.Close()
	if err != nil {
		panic("Failed to close network pipe")
	}
}
