package sfu

// Transport represents a transport
// that media can be sent over
type Transport interface {
	ID() string
	Routers() map[uint32]*Router
	AddSub(transport Transport)
	Subscribe(*Router, bool) error
	OnClose(f func())
	OnRouter(f func(router *Router))
	stats() string
}
