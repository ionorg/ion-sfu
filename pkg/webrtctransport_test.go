package sfu

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

func TestNewWebRTCTransport(t *testing.T) {
	type args struct {
		ctx     context.Context
		session *Session
		me      MediaEngine
		cfg     WebRTCTransportConfig
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "Must create a non nil webRTC transport",
			args: args{
				ctx:     context.Background(),
				session: NewSession("test"),
				me:      MediaEngine{},
				cfg:     WebRTCTransportConfig{},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewWebRTCTransport(tt.args.ctx, tt.args.session, tt.args.me, tt.args.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewWebRTCTransport() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.NotNil(t, got)
		})
	}
}

func TestWebRTCTransport_AddTransceiverFromKind(t *testing.T) {
	me := MediaEngine{}
	me.RegisterDefaultCodecs()

	type args struct {
		kind webrtc.RTPCodecType
		init []webrtc.RtpTransceiverInit
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "Must return transceiver without errors",
			args: args{
				kind: webrtc.RTPCodecTypeAudio,
				init: []webrtc.RtpTransceiverInit{{Direction: webrtc.RTPTransceiverDirectionRecvonly}},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := NewSession("test")
			p, err := NewWebRTCTransport(context.Background(), s, me, WebRTCTransportConfig{})
			assert.NoError(t, err)
			_, err = p.AddTransceiverFromKind(tt.args.kind, tt.args.init...)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddTransceiverFromKind() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestWebRTCTransport_Close(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	peer, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)
	s := NewSession("session")

	type fields struct {
		id      string
		ctx     context.Context
		cancel  context.CancelFunc
		pc      *webrtc.PeerConnection
		session *Session
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{
			name: "Must close inner peer connection and cancel context without errors.",
			fields: fields{
				id:      "test",
				ctx:     ctx,
				cancel:  cancel,
				pc:      peer,
				session: s,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				id:      tt.fields.id,
				ctx:     tt.fields.ctx,
				cancel:  tt.fields.cancel,
				pc:      tt.fields.pc,
				session: tt.fields.session,
			}
			s.transports[p.ID()] = p
			if err := p.Close(); (err != nil) != tt.wantErr {
				t.Errorf("Close() error = %v, wantErr %v", err, tt.wantErr)
			}
			assert.Error(t, ctx.Err())
			assert.Equal(t, 0, len(s.transports))
		})
	}
}

func TestWebRTCTransport_CreateAnswer(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	remoteTrack, err := remote.NewTrack(webrtc.DefaultPayloadTypeVP8, 1234, "video", "pion")
	assert.NoError(t, err)
	_, err = remote.AddTrack(remoteTrack)
	assert.NoError(t, err)

	offer, err := remote.CreateOffer(nil)
	assert.NoError(t, err)
	gatherComplete := webrtc.GatheringCompletePromise(remote)
	err = remote.SetLocalDescription(offer)
	assert.NoError(t, err)
	<-gatherComplete
	err = sfu.SetRemoteDescription(offer)
	assert.NoError(t, err)

	type fields struct {
		pc *webrtc.PeerConnection
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{
			name: "Must return answer without errors",
			fields: fields{
				pc: sfu,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				pc: tt.fields.pc,
			}
			_, err := p.CreateAnswer()
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateAnswer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestWebRTCTransport_CreateOffer(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)

	remoteTrack, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, 1234, "video", "pion")
	assert.NoError(t, err)
	_, err = sfu.AddTrack(remoteTrack)
	assert.NoError(t, err)

	type fields struct {
		pc *webrtc.PeerConnection
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{
			name:    "Must return offer without errors",
			fields:  fields{pc: sfu},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				pc: tt.fields.pc,
			}
			_, err := p.CreateOffer()
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateOffer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestWebRTCTransport_GetRouter(t *testing.T) {
	type fields struct {
		routers map[string]Router
	}
	type args struct {
		trackID string
	}

	router := &router{}

	tests := []struct {
		name   string
		fields fields
		args   args
		want   Router
	}{
		{
			name: "Must return router by ID",
			fields: fields{
				routers: map[string]Router{"test": router},
			},
			args: args{
				trackID: "test",
			},
			want: router,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				routers: tt.fields.routers,
			}
			if got := p.GetRouter(tt.args.trackID); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetRouter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebRTCTransport_ID(t *testing.T) {
	type fields struct {
		id string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name: "Must return current ID",
			fields: fields{
				id: "test",
			},
			want: "test",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				id: tt.fields.id,
			}
			if got := p.ID(); got != tt.want {
				t.Errorf("ID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebRTCTransport_LocalDescription(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)

	remoteTrack, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, 1234, "video", "pion")
	assert.NoError(t, err)
	_, err = sfu.AddTrack(remoteTrack)
	assert.NoError(t, err)

	offer, err := sfu.CreateOffer(nil)
	assert.NoError(t, err)
	gatherComplete := webrtc.GatheringCompletePromise(sfu)
	err = sfu.SetLocalDescription(offer)
	assert.NoError(t, err)
	<-gatherComplete
	targetLD := sfu.LocalDescription()

	type fields struct {
		pc *webrtc.PeerConnection
	}
	tests := []struct {
		name   string
		fields fields
		want   *webrtc.SessionDescription
	}{
		{
			name: "Must return current peer local description",
			fields: fields{
				pc: sfu,
			},
			want: targetLD,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				pc: tt.fields.pc,
			}
			if got := p.LocalDescription(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LocalDescription() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebRTCTransport_OnConnectionStateChange(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)

	type fields struct {
		pc *webrtc.PeerConnection
	}
	type args struct {
		f func(webrtc.PeerConnectionState)
	}

	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "Must set peer connection state callback",
			fields: fields{
				pc: sfu,
			},
			args: args{
				f: func(state webrtc.PeerConnectionState) {
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				pc: tt.fields.pc,
			}
			//TODO: Try to force the callback
			p.OnConnectionStateChange(tt.args.f)
		})
	}
}

func TestWebRTCTransport_OnDataChannel(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)
	_, err = remote.CreateDataChannel("data", &webrtc.DataChannelInit{})
	assert.NoError(t, err)
	// Register channel opening handling
	dcChan := make(chan struct{}, 1)

	type fields struct {
		pc *webrtc.PeerConnection
	}
	type args struct {
		f func(*webrtc.DataChannel)
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "Must set and call on data channel method",
			fields: fields{
				pc: sfu,
			},
			args: args{
				f: func(_ *webrtc.DataChannel) {
					dcChan <- struct{}{}
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				pc: tt.fields.pc,
			}
			p.OnDataChannel(tt.args.f)
			err = signalPair(remote, sfu)
			assert.NoError(t, err)
			tmr := time.NewTimer(5000 * time.Millisecond)
		testLoop:
			for {
				select {
				case <-tmr.C:
					t.Fatal("onDataChannel not called")
				case <-dcChan:
					tmr.Stop()
					break testLoop
				}
			}
		})
	}
	_ = sfu.Close()
	_ = remote.Close()
}

func TestWebRTCTransport_OnNegotiationNeeded(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	remoteTrack, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, 1234, "video", "pion")
	assert.NoError(t, err)
	_, err = sfu.AddTrack(remoteTrack)
	assert.NoError(t, err)

	err = signalPair(sfu, remote)

	negChan := make(chan struct{}, 1)

	type fields struct {
		pc *webrtc.PeerConnection
	}
	type args struct {
		f func()
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "Must set and call func on negotiation",
			fields: fields{
				pc: sfu,
			},
			args: args{
				f: func() {
					negChan <- struct{}{}
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				pc: tt.fields.pc,
			}
			p.OnNegotiationNeeded(tt.args.f)

			senderTrack, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, 5678, "video", "pion")
			assert.NoError(t, err)
			_, err = sfu.AddTrack(senderTrack)
			assert.NoError(t, err)
			tmr := time.NewTimer(5000 * time.Millisecond)
		testLoop:
			for {
				select {
				case <-tmr.C:
					t.Fatal("onNegotiation not called")
				case <-negChan:
					tmr.Stop()
					break testLoop
				}
			}
		})
	}
	_ = sfu.Close()
	_ = remote.Close()
}

func TestWebRTCTransport_OnTrack(t *testing.T) {
	type args struct {
		f func(*webrtc.Track, *webrtc.RTPReceiver)
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "Must set on track func",
			args: args{
				f: func(_ *webrtc.Track, _ *webrtc.RTPReceiver) {
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{}
			p.OnTrack(tt.args.f)
			assert.NotNil(t, p.onTrackHandler)
		})
	}
}

func TestWebRTCTransport_Routers(t *testing.T) {
	type fields struct {
		routers map[string]Router
	}

	routers := map[string]Router{"test": &router{}}

	tests := []struct {
		name   string
		fields fields
		want   map[string]Router
	}{
		{
			name: "Must return current map of routers",
			fields: fields{
				routers: routers,
			},
			want: routers,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				routers: tt.fields.routers,
			}
			if got := p.Routers(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Routers() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebRTCTransport_SetLocalDescription(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)
	offer, err := sfu.CreateOffer(nil)
	assert.NoError(t, err)

	type fields struct {
		pc *webrtc.PeerConnection
	}
	type args struct {
		desc webrtc.SessionDescription
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			name: "Must set local description on peer",
			fields: fields{
				pc: sfu,
			},
			args:    args{desc: offer},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				pc: tt.fields.pc,
			}
			if err := p.SetLocalDescription(tt.args.desc); (err != nil) != tt.wantErr {
				t.Errorf("SetLocalDescription() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWebRTCTransport_SetRemoteDescription(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)
	remoteTrack, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, 1234, "video", "pion")
	assert.NoError(t, err)
	_, err = sfu.AddTrack(remoteTrack)
	assert.NoError(t, err)

	offer, err := sfu.CreateOffer(nil)
	assert.NoError(t, err)
	gatherComplete := webrtc.GatheringCompletePromise(sfu)
	err = sfu.SetLocalDescription(offer)
	assert.NoError(t, err)
	<-gatherComplete
	err = remote.SetRemoteDescription(*sfu.LocalDescription())
	assert.NoError(t, err)
	answer, err := remote.CreateAnswer(nil)
	assert.NoError(t, err)
	err = remote.SetLocalDescription(answer)
	assert.NoError(t, err)

	type fields struct {
		pc *webrtc.PeerConnection
	}
	type args struct {
		desc webrtc.SessionDescription
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			name: "Must set remote description without errors",
			fields: fields{
				pc: sfu,
			},
			args: args{
				desc: answer,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				pc: tt.fields.pc,
			}
			if err := p.SetRemoteDescription(tt.args.desc); (err != nil) != tt.wantErr {
				t.Errorf("SetRemoteDescription() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWebRTCTransport_AddSender(t *testing.T) {
	type fields struct {
		senders map[string][]Sender
	}
	type args struct {
		streamID string
		sender   Sender
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name:   "Must add sender to given stream ID",
			fields: fields{senders: map[string][]Sender{}},
			args: struct {
				streamID string
				sender   Sender
			}{streamID: "test", sender: &SimpleSender{}},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				senders: tt.fields.senders,
			}
			p.AddSender(tt.args.streamID, tt.args.sender)
			assert.Equal(t, 1, len(p.senders))
			assert.Equal(t, 1, len(p.senders[tt.args.streamID]))
		})
	}
}

func TestWebRTCTransport_GetSenders(t *testing.T) {
	type fields struct {
		senders map[string][]Sender
	}
	type args struct {
		streamID string
	}
	sdrs := map[string][]Sender{"test": {&SimpleSender{}, &SimulcastSender{}}}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []Sender
	}{
		{
			name: "Must return an array of senders from given stream ID",
			fields: fields{
				senders: sdrs,
			},
			args: args{
				streamID: "test",
			},
			want: sdrs["test"],
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			p := &WebRTCTransport{
				senders: tt.fields.senders,
			}
			if got := p.GetSenders(tt.args.streamID); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetSenders() = %v, want %v", got, tt.want)
			}
		})
	}
}
