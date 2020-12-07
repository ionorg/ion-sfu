package sfu

import (
	"encoding/json"

	log "github.com/pion/ion-log"
	"github.com/pion/webrtc/v3"
)

const (
	apiChannelLabel = "ion-sfu"

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

func handleAPICommand(s *Subscriber, dc *webrtc.DataChannel) {
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		srm := &setRemoteMedia{}
		if err := json.Unmarshal(msg.Data, srm); err != nil {
			log.Errorf("Unmarshal api command err: %v", err)
			return
		}
		downTracks := s.GetDownTracks(srm.StreamID)

		for _, dt := range downTracks {
			switch dt.Kind() {
			case webrtc.RTPCodecTypeAudio:
				dt.Mute(!srm.Audio)
			case webrtc.RTPCodecTypeVideo:
				switch srm.Video {
				case videoHighQuality:
					dt.Mute(false)
					dt.SwitchSpatialLayer(2)
				case videoMediumQuality:
					dt.Mute(false)
					dt.SwitchSpatialLayer(1)
				case videoLowQuality:
					dt.Mute(false)
					dt.SwitchSpatialLayer(0)
				case videoMuted:
					dt.Mute(true)
				}
			}
		}
	})
}
