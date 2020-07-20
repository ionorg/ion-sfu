package sfu

import (
	"time"

	"github.com/lucsky/cuid"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v2"
)

const (
	statCycle = 6 * time.Second
)

// Peer represents a sfu peer connection
type Peer struct {
	id            string
	pc            *webrtc.PeerConnection
	me            *MediaEngine
	onTrackHander func(*webrtc.Track)
	onRecvHander  func(Receiver)
}

// NewPeer creates a new Peer
func NewPeer(offer webrtc.SessionDescription) (*Peer, error) {
	// We make our own mediaEngine so we can place the sender's codecs in it.  This because we must use the
	// dynamic media type from the sender in our answer. This is not required if we are the offerer
	me := MediaEngine{}
	if err := me.PopulateFromSDP(offer); err != nil {
		return nil, errSdpParseFailed
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(me.MediaEngine), webrtc.WithSettingEngine(setting))
	pc, err := api.NewPeerConnection(cfg)

	if err != nil {
		log.Errorf("NewPeer error: %v", err)
		return nil, errPeerConnectionInitFailed
	}

	p := &Peer{
		id: cuid.New(),
		pc: pc,
		me: &me,
	}

	pc.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		log.Infof("Peer %s got remote track %v", p.id, track)
		var recv Receiver
		switch track.Kind() {
		case webrtc.RTPCodecTypeVideo:
			recv = NewVideoReceiver(track)
		case webrtc.RTPCodecTypeAudio:
			recv = NewAudioReceiver(track)
		}

		_, err := p.pc.AddTransceiver(recv.Track().Kind(), webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
		if err != nil {
			log.Errorf("AddReceiver error: pc.AddTransceiver %v", err)
			return
		}

		go p.sendRTCP(recv)

		p.onRecvHander(recv)

		if p.onTrackHander != nil {
			p.onTrackHander(recv.Track())
		}
	})

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		switch connectionState {
		case webrtc.ICEConnectionStateDisconnected:
			log.Infof("webrtc ice disconnected for peer: %s", p.id)
		case webrtc.ICEConnectionStateFailed:
			fallthrough
		case webrtc.ICEConnectionStateClosed:
			log.Infof("webrtc ice closed for peer: %s", p.id)
			p.Close()
		}
	})

	return p, nil
}

// Answer an offer
func (p *Peer) Answer(offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	err := p.pc.SetRemoteDescription(offer)
	if err != nil {
		log.Errorf("Publish error: p.pc.SetRemoteDescription %v", err)
		return webrtc.SessionDescription{}, err
	}

	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		log.Errorf("Publish error: p.pc.CreateAnswer answer=%v err=%v", answer, err)
		return webrtc.SessionDescription{}, err
	}

	err = p.pc.SetLocalDescription(answer)
	if err != nil {
		log.Errorf("Publish error: p.pc.SetLocalDescription answer=%v err=%v", answer, err)
		return webrtc.SessionDescription{}, err
	}

	return answer, nil
}

// OnRecv handler called when a track is added
func (p *Peer) onRecv(f func(Receiver)) {
	p.onRecvHander = f
}

// OnTrack handler called when a track is added
func (p *Peer) OnTrack(f func(*webrtc.Track)) {
	p.onTrackHander = f
}

// AddICECandidate to peer connection
func (p *Peer) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	return p.pc.AddICECandidate(candidate)
}

// OnICECandidate handler
func (p *Peer) OnICECandidate(handler func(c *webrtc.ICECandidate)) {
	p.pc.OnICECandidate(handler)
}

// NewSender on this peer
func (p *Peer) NewSender(track *webrtc.Track) (*Sender, error) {
	pt, ok := p.me.GetPayloadType(track.Codec().Name)

	if !ok {
		log.Errorf("Error mapping payload type")
		return nil, errPtNotSupported
	}

	track, err := p.pc.NewTrack(pt, track.SSRC(), track.ID(), track.Label())

	if err != nil {
		log.Errorf("Error creating track")
		return nil, err
	}

	trans, err := p.pc.AddTransceiverFromTrack(track, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
		SendEncodings: []webrtc.RTPEncodingParameters{{
			RTPCodingParameters: webrtc.RTPCodingParameters{SSRC: track.SSRC(), PayloadType: pt},
		}},
	})

	send := NewSender(track, trans)
	return send, nil
}

// ID of peer
func (p *Peer) ID() string {
	return p.id
}

// Close peer
func (p *Peer) Close() error {
	return p.pc.Close()
}

func (p *Peer) sendRTCP(recv Receiver) {
	// TODO: stop on close
	for {
		pkt, err := recv.ReadRTCP()
		if err != nil {
			// TODO: do something
		}
		log.Tracef("sendRTCP %v", pkt)
		p.pc.WriteRTCP([]rtcp.Packet{pkt})
	}
}
