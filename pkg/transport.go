package sfu

// Transport represents a transport
// that media can be sent over
type Transport interface {
	ID() string
	GetRouter(string) *Router
	Routers() map[string]*Router
	NewSender(r *Router) (Sender, error)
	stats() string
}
