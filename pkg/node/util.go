package sfu

import (
	"fmt"
	"strconv"

	// sdp "github.com/pion/sdp/v2"

	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"
)

// func getSubPTForTrack(track *webrtc.Track, sdpObj *sdp.SDPInfo) uint8 {
// 	medias := sdpObj.GetMedias()
// 	log.Infof("Medias are %v", medias)

// 	transform := transport.PaylaodTransformMap()

// 	for _, m := range medias {
// 		for _, codec := range m.GetCodecs() {
// 			pt := codec.GetType()
// 			// 	If offer contains pub PT, use that
// 			if int(track.PayloadType()) == pt {
// 				return uint8(track.PayloadType())
// 			}
// 			// Otherwise look for first supported pt that can be transformed from pub
// 			log.Infof("%s %s", codec.GetCodec(), track.Codec().Name)
// 			if strings.EqualFold(codec.GetCodec(), track.Codec().Name) {
// 				for _, k := range transform[uint8(track.PayloadType())] {
// 					if uint8(pt) == k {
// 						return k
// 					}
// 				}
// 			}
// 		}
// 	}

// 	return 0
// }

func getCodecs(sdp sdp.SessionDescription) ([]uint8, error) {
	allowedCodecs := make([]uint8, 0)
	for _, md := range sdp.MediaDescriptions {
		for _, format := range md.MediaName.Formats {
			pt, err := strconv.Atoi(format)
			if err != nil {
				return nil, fmt.Errorf("format parse error")
			}

			if pt < 0 || pt > 255 {
				return nil, fmt.Errorf("payload type out of range: %d", pt)
			}

			payloadType := uint8(pt)
			payloadCodec, err := sdp.GetCodecForPayloadType(payloadType)
			if err != nil {
				return nil, fmt.Errorf("could not find codec for payload type %d", payloadType)
			}

			if md.MediaName.Media == "audio" && payloadCodec.Name == webrtc.Opus {
				allowedCodecs = append(allowedCodecs, payloadType)
				break
			} else {
				// skip 126 for pub, chrome sub will decode fail when H264 playload type is 126
				if payloadCodec.Name == webrtc.H264 && payloadType == 126 {
					continue
				}
				allowedCodecs = append(allowedCodecs, payloadType)
				break
			}
		}
	}

	return allowedCodecs, nil
}

// func getPubPTForTrack(videoCodec string, track *sdp.TrackInfo, sdpObj *sdp.SDPInfo) (pt uint8, codecName string) {
// 	media := sdpObj.GetMedia(track.GetMedia())
// 	codecs := media.GetCodecs()

// 	for payload, codec := range codecs {
// 		if track.GetMedia() == "audio" {
// 			codecName = strings.ToUpper(codec.GetCodec())
// 			if strings.EqualFold(codec.GetCodec(), webrtc.Opus) {
// 				pt = uint8(payload)
// 				break
// 			}
// 		} else if track.GetMedia() == "video" {
// 			codecName = strings.ToUpper(codec.GetCodec())
// 			//skip 126 for pub, chrome sub will decode fail when H264 playload type is 126
// 			if codecName == "H264" && payload == 126 {
// 				continue
// 			}
// 			if codecName == videoCodec {
// 				pt = uint8(payload)
// 				break
// 			}
// 		}
// 	}
// 	return
// }
