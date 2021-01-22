package sfu

import (
	"context"

	"github.com/pion/webrtc/v3"
)

type (
	Datachannel struct {
		label       string
		middlewares []func(MessageProcessor) MessageProcessor
		onMessage   func(ctx context.Context, msg webrtc.DataChannelMessage, in *webrtc.DataChannel, out []*webrtc.DataChannel)
	}

	ProcessArgs struct {
		Peer        *Peer
		Message     webrtc.DataChannelMessage
		DataChannel *webrtc.DataChannel
	}

	Middlewares []func(MessageProcessor) MessageProcessor

	MessageProcessor interface {
		Process(ctx context.Context, args ProcessArgs)
	}

	ProcessFunc func(ctx context.Context, args ProcessArgs)

	ChainHandler struct {
		Middlewares Middlewares
		Last        MessageProcessor
		current     MessageProcessor
	}
)

func (dc *Datachannel) Use(middlewares ...func(MessageProcessor) MessageProcessor) {
	dc.middlewares = append(dc.middlewares, middlewares...)
}

func (dc *Datachannel) OnMessage(fn func(ctx context.Context, msg webrtc.DataChannelMessage,
	in *webrtc.DataChannel, out []*webrtc.DataChannel)) {
	dc.onMessage = fn
}

func noOpProcess() MessageProcessor {
	return ProcessFunc(func(_ context.Context, _ ProcessArgs) {
	})
}

func (p ProcessFunc) Process(ctx context.Context, args ProcessArgs) {
	p(ctx, args)
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

func (c *ChainHandler) Process(ctx context.Context, args ProcessArgs) {
	c.current.Process(ctx, args)
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
