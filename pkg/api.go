package sfu

import (
	"encoding/json"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/webrtc/v3"
)

const (
	channelLabel = "ion-sfu"

	videoHighQuality   = "high"
	videoMediumQuality = "medium"
	videoLowQuality    = "low"
	videoMuted         = "none"
)

type setRemoteMedia struct {
	StreamID string `json:"streamId"`
	Video    string `json:"video"`
	Audio    bool   `json:"audio"`
}

func handleApiCommand(t *WebRTCTransport, dc *webrtc.DataChannel) {
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		srm := &setRemoteMedia{}
		if err := json.Unmarshal(msg.Data, srm); err != nil {
			log.Errorf("Unmarshal api command err: %v", err)
			return
		}
		senders := t.GetSenders(srm.StreamID)

		for _, sender := range senders {
			switch sender.Kind() {
			case webrtc.RTPCodecTypeAudio:
				sender.Muted(srm.Audio)
			case webrtc.RTPCodecTypeVideo:
				switch srm.Video {
				case videoHighQuality:
					sender.SwitchSpatialLayer(3)
				case videoMediumQuality:
					sender.SwitchSpatialLayer(2)
				case videoLowQuality:
					sender.SwitchSpatialLayer(1)
				case videoMuted:
					sender.Muted(true)
				}
			}
		}
	})
}
