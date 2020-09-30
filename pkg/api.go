package sfu

import (
	"encoding/json"

	"github.com/pion/ion-sfu/pkg/log"
)

// All available data channels commands for SFU, may reflect only changes un current sub
// this is not intended to force remove,mute, etc. to other publisher tracks.
// Data channel commands are received in ion-sfu data channel. So it mus be considered
// a reserved name
const (
	// Reserved data channel streamID for sfu commands
	ChannelLabel = "ion-sfu"

	// Mute command
	// Client: Send intent to mute the stream
	// Server: Send confirmation that stream is muted
	muteCommand = "mute"

	// Unmute command
	// Client: Send intent to unmute the stream
	// Server: Send confirmation that stream is muted
	unmuteCommand = "unmute"

	// Best Quality Command
	// Client: Send intent to get best quality of the stream
	// Server: Send confirmation that stream is in best quality,
	// send an error if stream does not support simulcast/SVC
	// NOTE: client may not get best quality is bw is a limitation.
	forceBestQuality = "bestQuality"

	// Lowest Quality Command
	// Client: Send intent to get lowest quality of the stream
	// Server: Send confirmation that stream is in lowest quality,
	// send an error if stream does not support simulcast/SVC
	forceLowerQuality = "lowestQuality"
)

//DataChannelCommand is the base command struct for all subscribers
// requests.
type DataChannelCommand struct {
	ID       string `json:"id"`
	Cmd      string `json:"cmd"`
	StreamID string `json:"stream"`
}

func HandleApiCommand(t *WebRTCTransport, msg []byte) {
	dcc := &DataChannelCommand{}
	if err := json.Unmarshal(msg, dcc); err != nil {
		log.Errorf("Unmarshal api command err: %v", err)
		return
	}

	switch dcc.Cmd {
	case muteCommand:
	case unmuteCommand:
	case forceBestQuality:
	case forceLowerQuality:
	}
}
