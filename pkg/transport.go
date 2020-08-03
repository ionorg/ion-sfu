package sfu

import "github.com/pion/webrtc/v3"

// Transport represents a transport
// that media can be sent over
type Transport interface {
	ID() string
	Routers() map[uint32]*Router
	AddSub(transport Transport)
	NewSender(*webrtc.Track) (Sender, error)
	OnClose(f func())
	OnRouter(f func(router *Router))
	stats() string
}
