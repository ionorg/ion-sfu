package sfu

import "net"

type Settings struct {
	// FeedbackDatachannelMiddlewares is used to process messages from client to fire a reaction
	// on SFU, this messages are not forwarded back to other users.
	// The data channel will be attached to Subscribers and negotiated on join.
	// Map key is the channel name
	FeedbackDatachannelMiddlewares map[string][]func(p MessageProcessor) MessageProcessor
	// FanOutDatachannelMiddleware is used to process messages from clients before forwarding
	// them to other users.
	// Data channels will not be created until the clients request them.
	// Map key is the channel name
	FanOutDatachannelMiddlewares map[string][]func(p MessageProcessor) MessageProcessor
	// TurnAuth is a callback to add custom authorization on turn join.
	TurnAuth func(username, realm string, srcAddr net.Addr) ([]byte, bool)
}
