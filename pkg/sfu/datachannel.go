package sfu

import (
	"github.com/pion/webrtc/v3"
)

type (
	Middlewares []func(processor MessageProcessor) MessageProcessor

	MessageProcessor interface {
		Process(peer *Peer, dc *webrtc.DataChannel, msg webrtc.DataChannelMessage)
	}

	ProcessFunc func(peer *Peer, dc *webrtc.DataChannel, msg webrtc.DataChannelMessage)

	ChainHandler struct {
		Middlewares Middlewares
		Last        MessageProcessor
		current     MessageProcessor
	}
)

func noOpProcess() MessageProcessor {
	return ProcessFunc(func(_ *Peer, _ *webrtc.DataChannel, _ webrtc.DataChannelMessage) {
	})
}

func (p ProcessFunc) Process(peer *Peer, dc *webrtc.DataChannel, msg webrtc.DataChannelMessage) {
	p(peer, dc, msg)
}

func (mws Middlewares) Process(h MessageProcessor) MessageProcessor {
	return &ChainHandler{mws, h, chain(mws, h)}
}

func (mws Middlewares) ProcessFunc(h MessageProcessor) MessageProcessor {
	return &ChainHandler{mws, h, chain(mws, h)}
}

// NewDCChain returns a new Chain interceptor.
func NewDCChain(m []func(p MessageProcessor) MessageProcessor) Middlewares {
	return Middlewares(m)
}

func (c *ChainHandler) Process(peer *Peer, dc *webrtc.DataChannel, msg webrtc.DataChannelMessage) {
	c.current.Process(peer, dc, msg)
}

func chain(mws []func(processor MessageProcessor) MessageProcessor, last MessageProcessor) MessageProcessor {
	if len(mws) == 0 {
		return last
	}
	h := mws[len(mws)-1](last)
	for i := len(mws) - 2; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
