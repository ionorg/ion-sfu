package relay

import (
	"encoding/json"

	"github.com/pion/webrtc/v3"
)

type Stream interface {
	Signal() ([]byte, error)
	Stop() error
}

type SendStream interface {
	Sender() *webrtc.RTPSender
	Start(remoteSignal []byte) error
	Stream
}

type ReceiveStream interface {
	Receiver() *webrtc.RTPReceiver
	Start() error
	Stream
}

type receiveStream struct {
	relay        *Relay
	params       webrtc.RTPReceiveParameters
	receiver     *webrtc.RTPReceiver
	localSignal  Signal
	remoteSignal Signal
}

func (r receiveStream) Receiver() *webrtc.RTPReceiver {
	return r.receiver
}

func (r receiveStream) Signal() ([]byte, error) {
	return json.Marshal(&r.localSignal)
}

func (r receiveStream) Start() error {
	if err := r.relay.ice.SetRemoteCandidates(r.remoteSignal.ICECandidates); err != nil {
		return err
	}

	iceRole := webrtc.ICERoleControlled
	if err := r.relay.ice.Start(nil, r.remoteSignal.ICEParameters, &iceRole); err != nil {
		return err
	}

	if err := r.relay.dtls.Start(r.remoteSignal.DTLSParameters); err != nil {
		return err
	}

	return r.receiver.Receive(webrtc.RTPReceiveParameters{Encodings: []webrtc.RTPDecodingParameters{
		{
			webrtc.RTPCodingParameters{
				SSRC:        r.remoteSignal.Encodings.SSRC,
				PayloadType: r.remoteSignal.Encodings.PayloadType,
			},
		},
	}})
}

func (r receiveStream) Stop() error {
	return r.receiver.Stop()
}

type sendStream struct {
	relay  *Relay
	signal Signal
	params webrtc.RTPSendParameters
	sender *webrtc.RTPSender
}

func (s *sendStream) Signal() ([]byte, error) {
	return json.Marshal(&s.signal)
}

func (s *sendStream) Start(signal []byte) error {
	remoteSignal := Signal{}
	if err := json.Unmarshal(signal, &remoteSignal); err != nil {
		return err
	}

	if err := s.relay.ice.SetRemoteCandidates(remoteSignal.ICECandidates); err != nil {
		return err
	}

	iceRole := webrtc.ICERoleControlling
	if err := s.relay.ice.Start(nil, remoteSignal.ICEParameters, &iceRole); err != nil {
		return err
	}

	if err := s.relay.dtls.Start(remoteSignal.DTLSParameters); err != nil {
		return err
	}

	return s.sender.Send(s.params)
}

func (s *sendStream) Stop() error {
	return s.sender.Stop()
}

func (s *sendStream) Sender() *webrtc.RTPSender {
	return s.sender
}
