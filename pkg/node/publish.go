package sfu

import (
	"github.com/lucsky/cuid"
	"github.com/pion/webrtc/v2"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/media"
	"github.com/pion/ion-sfu/pkg/rtc"
	transport "github.com/pion/ion-sfu/pkg/rtc/transport"
)

// Publish a webrtc stream
func Publish(offer webrtc.SessionDescription) (string, *webrtc.PeerConnection, *webrtc.SessionDescription, error) {
	mid := cuid.New()

	// We make our own mediaEngine so we can place the sender's codecs in it.  This because we must use the
	// dynamic media type from the sender in our answer. This is not required if we are the offerer
	me := media.Engine{}
	if err := me.PopulateFromSDP(offer); err != nil {
		return "", nil, nil, errSdpParseFailed
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine), webrtc.WithSettingEngine(setting))
	pc, err := api.NewPeerConnection(cfg)

	if err != nil {
		log.Errorf("Publish error: %v", err)
		return "", nil, nil, errPeerConnectionInitFailed
	}

	pub := transport.NewWebRTCTransport(mid, pc, &me)
	if pub == nil {
		return "", nil, nil, errWebRTCTransportInitFailed
	}

	router := rtc.AddRouter(mid)

	pc.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		log.Infof("Publish: Got track %v", track)
		pub.AddInTrack(track)

		for _, t := range router.GetSubs() {
			sub := t.(*transport.WebRTCTransport)
			_, err := sub.AddOutTrack(mid, track)
			if err != nil {
				log.Errorf("Error adding out track to sub: %s", err)
			}
		}
	})

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		switch connectionState {
		case webrtc.ICEConnectionStateDisconnected:
			log.Infof("webrtc ice disconnected for mid: %s", mid)
		case webrtc.ICEConnectionStateFailed:
			fallthrough
		case webrtc.ICEConnectionStateClosed:
			log.Infof("webrtc ice closed for mid: %s", mid)
			pub.Close()
		}
	})

	err = pc.SetRemoteDescription(offer)
	if err != nil {
		log.Errorf("Publish error: pc.SetRemoteDescription %v", err)
		return "", nil, nil, err
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		log.Errorf("Publish error: pc.CreateAnswer answer=%v err=%v", answer, err)
		return "", nil, nil, err
	}

	err = pc.SetLocalDescription(answer)
	if err != nil {
		log.Errorf("Publish error: pc.SetLocalDescription answer=%v err=%v", answer, err)
		return "", nil, nil, err
	}

	router.AddPub(pub)

	log.Debugf("Publish: answer => %v", answer)
	return mid, pc, &answer, nil
}
