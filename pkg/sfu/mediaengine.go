package sfu

import (
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
)

const frameMarking = "urn:ietf:params:rtp-hdrext:framemarking"

func getPublisherMediaEngine() (*webrtc.MediaEngine, error) {
	me := &webrtc.MediaEngine{}
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1", RTCPFeedback: nil},
		PayloadType:        111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	videoRTCPFeedback := []webrtc.RTCPFeedback{
		{Type: webrtc.TypeRTCPFBGoogREMB, Parameter: ""},
		{Type: webrtc.TypeRTCPFBCCM, Parameter: "fir"},
		{Type: webrtc.TypeRTCPFBNACK, Parameter: ""},
		{Type: webrtc.TypeRTCPFBNACK, Parameter: "pli"},
	}
	for _, codec := range []webrtc.RTPCodecParameters{
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000, RTCPFeedback: videoRTCPFeedback},
			PayloadType:        96,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP9, ClockRate: 90000, SDPFmtpLine: "profile-id=0", RTCPFeedback: videoRTCPFeedback},
			PayloadType:        98,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP9, ClockRate: 90000, SDPFmtpLine: "profile-id=1", RTCPFeedback: videoRTCPFeedback},
			PayloadType:        100,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f", RTCPFeedback: videoRTCPFeedback},
			PayloadType:        102,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42001f", RTCPFeedback: videoRTCPFeedback},
			PayloadType:        127,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f", RTCPFeedback: videoRTCPFeedback},
			PayloadType:        125,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42e01f", RTCPFeedback: videoRTCPFeedback},
			PayloadType:        108,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=640032", RTCPFeedback: videoRTCPFeedback},
			PayloadType:        123,
		},
	} {
		if err := me.RegisterCodec(codec, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	}

	for _, extension := range []string{
		sdp.SDESMidURI,
		sdp.SDESRTPStreamIDURI,
		sdp.TransportCCURI,
		frameMarking,
	} {
		if err := me.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: extension}, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	}
	for _, extension := range []string{
		sdp.SDESMidURI,
		sdp.SDESRTPStreamIDURI,
		sdp.AudioLevelURI,
	} {
		if err := me.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: extension}, webrtc.RTPCodecTypeAudio); err != nil {
			return nil, err
		}
	}

	return me, nil
}

func getSubscriberMediaEngine() (*webrtc.MediaEngine, error) {
	me := &webrtc.MediaEngine{}
	return me, nil
}
