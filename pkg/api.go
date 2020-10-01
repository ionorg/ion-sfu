package sfu

import (
	"encoding/json"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/webrtc/v3"
)

// All available data channels commands for SFU, may reflect only changes on caller,
// this is not intended to force remove,mute, etc. to other publisher tracks.
// Data channel commands are received in ion-sfu data channel. So it must be considered
// a reserved name
const (
	// Reserved data channel streamID for sfu commands
	channelLabel = "ion-sfu"

	// Mute command
	// Direction: Client -> SFU -> Client
	// Client: Send intent to mute the stream
	// Server: Send confirmation that stream is muted
	muteCommand = "mute"

	// Unmute command
	// Direction: Client -> SFU -> Client
	// Client: Send intent to unmute the stream
	// Server: Send confirmation that stream is un-muted
	unmuteCommand = "unmute"

	// Best Quality Command
	// Direction: Client -> SFU -> Client
	// Client: Send intent to get best quality of the stream
	// Server: Send confirmation that stream is in best quality,
	// send an error if stream does not support simulcast/SVC
	// NOTE: client may not get best quality is bw is a limitation.
	forceBestQuality = "bestQuality"

	// Lowest Quality Command
	// Direction: Client -> SFU -> Client
	// Client: Send intent to get lowest quality of the stream
	// Server: Send confirmation that stream is in lowest quality,
	// send an error if stream does not support simulcast/SVC
	forceLowerQuality = "lowestQuality"

	// Set Speaker
	// Direction: SFU -> Client
	// Server: Sends information about current speaker
	// Client: Should react to speaker if required
	setSpeaker = "speaker"
)

// DataChannelCommand is the base command struct for all subscribers
// requests.
type DataChannelCommand struct {
	ID       string `json:"id"`
	Cmd      string `json:"cmd"`
	StreamID string `json:"stream"`
}

// HandleApiCommand handle all request from sub, all tracks from stream id
// will be affected by this commands
func HandleApiCommand(t *WebRTCTransport, dc *webrtc.DataChannel) {
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		dcc := &DataChannelCommand{}
		if err := json.Unmarshal(msg.Data, dcc); err != nil {
			log.Errorf("Unmarshal api command err: %v", err)
			return
		}
		senders := t.GetSenders(dcc.StreamID)

		switch dcc.Cmd {
		case muteCommand:
			for _, sender := range senders {
				sender.Muted(true)
			}
		case unmuteCommand:
			println("un muting!!!")
			for _, sender := range senders {
				sender.Muted(false)
			}
		case forceBestQuality:
		case forceLowerQuality:
		}
	})
}
