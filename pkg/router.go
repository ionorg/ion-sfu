package sfu

import (
	"sync"
	"time"
)

var routerConfig RouterConfig

// Router defines a track rtp/rtcp router
type Router struct {
	tid       string
	mu        sync.RWMutex
	receivers [3 + 1]Receiver
	lastNack  int64
	simulcast bool
}

// NewRouter for routing rtp/rtcp packets
func NewRouter(tid string, hasSimulcast bool) *Router {
	r := &Router{
		tid:       tid,
		lastNack:  time.Now().Unix(),
		simulcast: hasSimulcast,
	}
	return r
}

func (r *Router) AddReceiver(recv Receiver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.receivers[recv.SpatialLayer()] = recv
}

func (r *Router) GetReceiver(layer uint8) Receiver {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.receivers[layer]
}

// AddSender to router
func (r *Router) AddSender(sub Sender) {
	r.receivers[sub.CurrentLayer()].AddSender(sub)
}

func (r *Router) SwitchSpatialLayer(currentLayer, targetLayer uint8, sub Sender) bool {
	currentRecv := r.GetReceiver(currentLayer)
	targetRecv := r.GetReceiver(targetLayer)
	if targetRecv != nil {
		// TODO do a more smart layer change
		currentRecv.DeleteSender(sub.ID())
		targetRecv.AddSender(sub)
		return true
	}
	return false
}

func (r *Router) stats() string {
	return ""
}
