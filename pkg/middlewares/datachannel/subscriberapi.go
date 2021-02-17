package datachannel

import (
	"context"
	"encoding/json"

	"github.com/pion/ion-sfu/pkg/sfu"
	"github.com/pion/webrtc/v3"
)

const (
	highValue   = "high"
	mediumValue = "medium"
	lowValue    = "low"
	mutedValue  = "none"
)

type setRemoteMedia struct {
	StreamID  string `json:"streamId"`
	Video     string `json:"video"`
	Framerate string `json:"framerate"`
	Audio     bool   `json:"audio"`
}

func SubscriberAPI(next sfu.MessageProcessor) sfu.MessageProcessor {
	return sfu.ProcessFunc(func(ctx context.Context, args sfu.ProcessArgs) {
		srm := &setRemoteMedia{}
		if err := json.Unmarshal(args.Message.Data, srm); err != nil {
			return
		}
		downTracks := args.Peer.Subscriber().GetDownTracks(srm.StreamID)
		for _, dt := range downTracks {
			switch dt.Kind() {
			case webrtc.RTPCodecTypeAudio:
				dt.Mute(!srm.Audio)
			case webrtc.RTPCodecTypeVideo:
				switch srm.Video {
				case highValue:
					dt.Mute(false)
					dt.SwitchSpatialLayer(2, true)
				case mediumValue:
					dt.Mute(false)
					dt.SwitchSpatialLayer(1, true)
				case lowValue:
					dt.Mute(false)
					dt.SwitchSpatialLayer(0, true)
				case mutedValue:
					dt.Mute(true)
				}
				switch srm.Framerate {
				case highValue:
					dt.SwitchTemporalLayer(2, true)
				case mediumValue:
					dt.SwitchTemporalLayer(1, true)
				case lowValue:
					dt.SwitchTemporalLayer(0, true)
				}
			}

		}
		next.Process(ctx, args)
	})
}
