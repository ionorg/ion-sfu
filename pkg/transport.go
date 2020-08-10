package sfu

import "github.com/pion/webrtc/v3"

// Transport represents a transport
// that media can be sent over
type Transport interface {
	ID() string
	GetRouter(uint32) *Router
	Routers() map[uint32]*Router
	NewSender(*webrtc.Track) (Sender, error)
	stats() string
}
