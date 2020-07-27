package sfu

import (
	"fmt"
	"sync"

	"github.com/pion/ion-sfu/pkg/log"
)

// Room represents a set of peers. Peers inside a room are
// automatically subscribed to each others tracks
type Room struct {
	id             string
	peers          map[string]*Peer
	peersLock      sync.RWMutex
	onCloseHandler func()
}

// NewRoom creates a new room
func NewRoom(id string) *Room {
	return &Room{
		id:    id,
		peers: make(map[string]*Peer),
	}
}

// AddPeer adds a peer to the room
func (r *Room) AddPeer(p *Peer) {
	r.peersLock.Lock()

	// Subscribe new peer to existing peers
	for _, peer := range r.peers {
		log.Infof("Peer %s add sub %s", peer.id, p.id)
		peer.AddSub(p)
	}

	r.peers[p.id] = p
	r.peersLock.Unlock()

	p.OnClose(func() {
		r.peersLock.Lock()
		delete(r.peers, p.id)
		// Remove peer subs from pubs
		for _, peer := range r.peers {
			for _, router := range peer.routers {
				router.DelSub(p.id)
			}
		}

		// Close room if no peers
		if len(r.peers) == 0 && r.onCloseHandler != nil {
			r.onCloseHandler()
		}
		r.peersLock.Unlock()
	})

	// New track router added to peer, subscribe
	// other peers in room to it
	p.OnRouter(func(router *Router) {
		r.peersLock.Lock()
		defer r.peersLock.Unlock()
		log.Debugf("on router %v", router)
		for pid, peer := range r.peers {
			// Don't sub to self
			if p.id == pid {
				continue
			}
			err := peer.Subscribe(router)
			if err != nil {
				log.Errorf("Error subscribing peer to router: %s", err)
			}
			if peer.onNegotiationNeededHandler != nil {
				log.Infof("debounced %s", peer.id)
				peer.onNegotiationNeededHandler()
			}
		}
	})
}

// OnClose called when room is closed
func (r *Room) OnClose(f func()) {
	r.onCloseHandler = f
}

func (r *Room) stats() string {
	info := fmt.Sprintf("\nroom: %s\n", r.id)

	r.peersLock.RLock()
	for _, peer := range r.peers {
		info += peer.stats()
	}
	r.peersLock.RUnlock()
	log.Infof(info)

	return info
}
