package sfu

import (
	"errors"

	"github.com/lucsky/cuid"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/media"
	"github.com/pion/ion-sfu/pkg/rtc"
	transport "github.com/pion/ion-sfu/pkg/rtc/transport"
	"github.com/pion/sdp/v2"
	"github.com/pion/webrtc/v2"
)

// Subscribe to a mid
func Subscribe(mid string, offer webrtc.SessionDescription) (string, *webrtc.PeerConnection, *webrtc.SessionDescription, error) {
	parsed := sdp.SessionDescription{}
	err := parsed.Unmarshal([]byte(offer.SDP))

	if err != nil {
		log.Debugf("Subscribe: err=%v sdp=%v", err, parsed)
		return "", nil, nil, errSdpParseFailed
	}

	log.Infof("Subscribe called: %v", parsed)
	router := rtc.GetRouter(mid)

	if router == nil {
		return "", nil, nil, errors.New("Subscribe: router not found")
	}

	pub := router.GetPub().(*transport.WebRTCTransport)

	me := media.Engine{}
	if err = me.PopulateFromSDP(offer); err != nil {
		return "", nil, nil, errSdpParseFailed
	}
	me.MapFromEngine(pub.MediaEngine())

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine), webrtc.WithSettingEngine(setting))
	pc, err := api.NewPeerConnection(cfg)

	if err != nil {
		log.Errorf("Subscribe error: %v", err)
		return "", nil, nil, errPeerConnectionInitFailed
	}

	sub := transport.NewWebRTCTransport(cuid.New(), pc, &me)

	if sub == nil {
		return "", nil, nil, errors.New("Subscribe: transport.NewWebRTCTransport failed")
	}

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		switch connectionState {
		case webrtc.ICEConnectionStateDisconnected:
			log.Infof("webrtc ice disconnected for mid: %s", mid)
		case webrtc.ICEConnectionStateFailed:
			fallthrough
		case webrtc.ICEConnectionStateClosed:
			log.Infof("webrtc ice closed for mid: %s", mid)
			sub.Close()
		}
	})

	// Add existing pub tracks to sub
	for ssrc, track := range pub.GetInTracks() {
		log.Debugf("AddTrack: codec:%s, ssrc:%d, streamID %s, trackID %s", track.Codec().MimeType, ssrc, mid, track.ID())
		_, err := sub.AddOutTrack(mid, track)
		if err != nil {
			log.Errorf("err=%v", err)
		}
	}

	err = pc.SetRemoteDescription(offer)
	if err != nil {
		log.Errorf("Subscribe error: pc.SetRemoteDescription %v", err)
		return "", nil, nil, err
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		log.Errorf("Subscribe error: pc.CreateAnswer answer=%v err=%v", answer, err)
		return "", nil, nil, err
	}

	err = pc.SetLocalDescription(answer)
	if err != nil {
		log.Errorf("Subscribe error: pc.SetLocalDescription answer=%v err=%v", answer, err)
		return "", nil, nil, err
	}

	router.AddSub(sub.ID(), sub)

	log.Debugf("Subscribe: mid %s, answer = %v", sub.ID(), answer)
	return sub.ID(), pc, &answer, nil
}
