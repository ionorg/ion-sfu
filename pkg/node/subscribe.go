package sfu

import (
	"errors"
	"strconv"
	"strings"

	"github.com/lucsky/cuid"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/rtc"
	transport "github.com/pion/ion-sfu/pkg/rtc/transport"
	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"
)

func getSubPayloadType(track *webrtc.Track, parsed sdp.SessionDescription) uint8 {
	transform := transport.PayloadTransformMap()
	for _, md := range parsed.MediaDescriptions {
		if md.MediaName.Media != "audio" && md.MediaName.Media != "video" {
			continue
		}

		for _, format := range md.MediaName.Formats {
			pt, err := strconv.Atoi(format)

			if err != nil {
				return 0
			}

			if pt < 0 || pt > 255 {
				return 0
			}

			payloadType := uint8(pt)

			// 	If offer contains pub payload type, use that
			if track.Codec().Name == payloadType {
				return payloadType
			}

			payloadCodec, err := parsed.GetCodecForPayloadType(payloadType)
			if err != nil {
				return 0
			}

			// Otherwise look for first supported pt that can be transformed from pub
			log.Infof("%s %s", payloadCodec.Name, track.Codec().Name)
			if strings.EqualFold(payloadCodec.Name, track.Codec().Name) {
				for _, k := range transform[track.PayloadType()] {
					if payloadType == k {
						return k
					}
				}
			}
		}
	}

	return 0
}

// Subscribe to a mid
func Subscribe(mid string, offer webrtc.SessionDescription) (*transport.WebRTCTransport, *webrtc.SessionDescription, error) {
	parsed := sdp.SessionDescription{}
	err := parsed.Unmarshal([]byte(offer.SDP))

	if err != nil {
		log.Debugf("subscribe->connect: err=%v sdp=%v", err, parsed)
		return nil, nil, errSdpParseFailed
	}

	log.Infof("subscribe->connect called: %v", parsed)
	router := rtc.GetRouter(mid)

	if router == nil {
		return nil, nil, errors.New("subscribe->connect: router not found")
	}

	pub := router.GetPub().(*transport.WebRTCTransport)

	rtcOptions := transport.RTCOptions{
		Subscribe: true,
		Ssrcpt:    make(map[uint32]uint8),
	}

	// We make our own mediaEngine so we can place the sender's codecs in it.  This because we must use the
	// dynamic media type from the sender in our answer. This is not required if we are the offerer
	mediaEngine := webrtc.MediaEngine{}
	if err = mediaEngine.PopulateFromSDP(offer); err != nil {
		return nil, nil, errors.New("subscribe->connect: failed to initialize media engine")
	}

	tracks := pub.GetInTracks()
	log.Debugf("Sub to pub with tracks %v", tracks)
	ssrcPTMap := make(map[uint32]uint8)
	allowedCodecs := make([]uint8, 0, len(tracks))

	for ssrc, track := range tracks {
		rtcOptions.Ssrcpt[ssrc] = uint8(track.PayloadType())

		// Find pt for track given track.Payload and sdp
		ssrcPTMap[ssrc] = getSubPayloadType(track, parsed)
		allowedCodecs = append(allowedCodecs, ssrcPTMap[ssrc])
	}

	// Set media engine codecs based on found pts
	log.Debugf("Allowed codecs %v", allowedCodecs)
	rtcOptions.Codecs = allowedCodecs

	sub := transport.NewWebRTCTransport(cuid.New(), rtcOptions)

	if sub == nil {
		return nil, nil, errors.New("subscribe->connect: transport.NewWebRTCTransport failed")
	}

	for ssrc, track := range tracks {
		// Get payload type from request track
		pt := track.PayloadType()
		if newPt, ok := ssrcPTMap[ssrc]; ok {
			// Override with "negotiated" PT
			pt = newPt
		}

		// I2AacsRLsZZriGapnvPKiKBcLi8rTrO1jOpq c84ded42-d2b0-4351-88d2-b7d240c33435
		//                streamID                        trackID
		log.Debugf("AddTrack: codec:%s, ssrc:%d, pt:%d, streamID %s, trackID %s", track.Codec().MimeType, ssrc, pt, pub.ID(), track.ID())
		_, err := sub.AddSendTrack(ssrc, pt, pub.ID(), track.ID())
		if err != nil {
			log.Errorf("err=%v", err)
		}
	}

	answer, err := sub.Answer(offer, rtcOptions)

	if err != nil {
		log.Debugf("subscribe->connect: error creating answer %v", err)
		return nil, nil, errWebRTCTransportAnswerFailed
	}

	router.AddSub(sub.ID(), sub)

	log.Debugf("subscribe->connect: mid %s, answer = %v", sub.ID(), answer)
	return sub, &answer, nil
}
