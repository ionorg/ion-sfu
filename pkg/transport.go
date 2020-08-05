package sfu

// Transport represents a transport
// that media can be sent over
type Transport interface {
	ID() string
	GetRouter(uint32) *Router
	Routers() map[uint32]*Router
	NewSender(Track) (Sender, error)
	stats() string
}
