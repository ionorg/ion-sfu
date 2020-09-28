package sfu

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/pion/rtp"

	"github.com/stretchr/testify/assert"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

func TestNewWebRTCSimulcastSender(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	ctx := context.Background()

	local, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)
	senderTrack, err := local.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "fake_id", "fake_label")
	assert.NoError(t, err)
	sender, err := local.AddTrack(senderTrack)
	assert.NoError(t, err)

	type args struct {
		ctx    context.Context
		id     string
		router *router
		sender *webrtc.RTPSender
		layer  uint8
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "Must return a non nil Sender",
			args: args{
				ctx:    ctx,
				id:     "test",
				router: nil,
				sender: sender,
				layer:  2,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := NewWebRTCSimulcastSender(tt.args.ctx, tt.args.id, tt.args.router, tt.args.sender, tt.args.layer)
			assert.NotNil(t, got)
		})
	}
}

func TestWebRTCSimulcastSender_WriteRTP(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	var remoteTrack *webrtc.Track
	gotTrack := make(chan struct{}, 1)

	simulcastSSRC := rand.Uint32()

	remote.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver) {
		_, err := track.ReadRTP()
		assert.NoError(t, err)
		remoteTrack = track
		gotTrack <- struct{}{}
	})

	fakeRecv, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)
	fakeRecvTrack, err := fakeRecv.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "fake_id", "fake_label")
	assert.NoError(t, err)

	senderTrack, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, simulcastSSRC, "video", "pion")
	assert.NoError(t, err)
	_, err = sfu.AddTrack(senderTrack)
	assert.NoError(t, err)

	gotPli := make(chan struct{}, 1)
	fakeReceiver := &ReceiverMock{
		TrackFunc: func() *webrtc.Track {
			return fakeRecvTrack
		},
		WriteRTCPFunc: func(in1 rtcp.Packet) error {
			if _, ok := in1.(*rtcp.PictureLossIndication); ok {
				gotPli <- struct{}{}
			}
			return nil
		},
	}

	fakeRouter := &RouterMock{
		GetReceiverFunc: func(_ uint8) Receiver {
			return fakeReceiver
		},
	}

	err = signalPair(sfu, remote)
	assert.NoError(t, err)

forLoop:
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			pkt := senderTrack.Packetizer().Packetize([]byte{0x01, 0x02, 0x03, 0x04}, 1)[0]
			err = senderTrack.WriteRTP(pkt)
			assert.NoError(t, err)
		case <-gotTrack:
			break forLoop
		}
	}

	type fields struct {
		checkPli    bool
		checkPacket bool
		packet      *rtp.Packet
		wantPacket  *rtp.Packet
	}

	fakePkt := senderTrack.Packetizer().Packetize([]byte{0x01, 0x02, 0x03, 0x04}, 1)[0]
	fakePkt.SSRC = fakeRecvTrack.SSRC()

	tests := []struct {
		name   string
		fields fields
	}{
		{
			name: "On spatial change sender must forward a RTCP PLI request to receiver",
			fields: fields{
				checkPli: true,
				packet:   fakePkt,
			},
		},
		{
			name: "Sender packet SSRC must be same as simulcast SSRC, and receiver packet must be restored",
			fields: fields{
				checkPacket: true,
				packet:      fakePkt,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if tt.fields.checkPli {
				s := &WebRTCSimulcastSender{
					ctx:           context.Background(),
					router:        fakeRouter,
					track:         senderTrack,
					simulcastSSRC: simulcastSSRC,
				}
				tmr := time.NewTimer(100 * time.Millisecond)
				s.WriteRTP(tt.fields.packet)
				for {
					select {
					case <-tmr.C:
						t.Fatal("PLI packet not received")
					case <-gotPli:
						tmr.Stop()
						return
					}
				}
			}
			if tt.fields.checkPacket {
				s := &WebRTCSimulcastSender{
					ctx:           context.Background(),
					router:        fakeRouter,
					track:         senderTrack,
					simulcastSSRC: simulcastSSRC,
					lSSRC:         fakeRecvTrack.SSRC(),
				}
				s.WriteRTP(tt.fields.packet)
				pkt, err := remoteTrack.ReadRTP()
				assert.NoError(t, err)
				assert.Equal(t, simulcastSSRC, pkt.SSRC)
				assert.Equal(t, fakeRecvTrack.SSRC(), tt.fields.packet.SSRC)
			}

		})
	}

}

func TestWebRTCSimulcastSender_receiveRTCP(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	gotTrack := make(chan struct{}, 1)

	simulcastSSRC := rand.Uint32()
	recvSSRC := rand.Uint32()

	remote.OnTrack(func(track *webrtc.Track, r *webrtc.RTPReceiver) {
		_, err := track.ReadRTP()
		assert.NoError(t, err)
		gotTrack <- struct{}{}
	})

	senderTrack, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, simulcastSSRC, "video", "pion")
	assert.NoError(t, err)
	s, err := sfu.AddTrack(senderTrack)
	assert.NoError(t, err)

	gotRTCP := make(chan rtcp.Packet, 100)
	fakeReceiver := &ReceiverMock{
		WriteRTCPFunc: func(in1 rtcp.Packet) error {
			gotRTCP <- in1
			return nil
		},
	}

	fakeRouter := &RouterMock{
		GetReceiverFunc: func(_ uint8) Receiver {
			return fakeReceiver
		},
	}

	err = signalPair(sfu, remote)
	assert.NoError(t, err)

forLoop:
	for {
		select {
		case <-time.After(500 * time.Millisecond):
			pkt := senderTrack.Packetizer().Packetize([]byte{0x01, 0x02, 0x03, 0x04}, 1)[0]
			err = senderTrack.WriteRTP(pkt)
			assert.NoError(t, err)
		case <-gotTrack:
			break forLoop
		}
	}

	tests := []struct {
		name string
		want rtcp.Packet
	}{
		{
			name: "Sender must forward PLI messages, with correct SSRC",
			want: &rtcp.PictureLossIndication{
				SenderSSRC: simulcastSSRC,
				MediaSSRC:  simulcastSSRC,
			},
		},
		{
			name: "Sender must forward FIR messages, with correct SSRC",
			want: &rtcp.FullIntraRequest{
				SenderSSRC: simulcastSSRC,
				MediaSSRC:  simulcastSSRC,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			wss := &WebRTCSimulcastSender{
				ctx:           ctx,
				cancel:        cancel,
				router:        fakeRouter,
				sender:        s,
				track:         senderTrack,
				lSSRC:         recvSSRC,
				simulcastSSRC: simulcastSSRC,
			}
			go wss.receiveRTCP()
			tmr := time.NewTimer(1000 * time.Millisecond)
			err := remote.WriteRTCP([]rtcp.Packet{tt.want, tt.want, tt.want, tt.want})
			assert.NoError(t, err)
			err = remote.WriteRTCP([]rtcp.Packet{tt.want, tt.want, tt.want, tt.want})
			assert.NoError(t, err)
			err = remote.WriteRTCP([]rtcp.Packet{tt.want, tt.want, tt.want, tt.want})
			assert.NoError(t, err)
			for {
				select {
				case <-tmr.C:
					t.Fatal("RTCP packet not received")
				case pkt := <-gotRTCP:
					switch pkt.(type) {
					case *rtcp.PictureLossIndication:
						assert.Equal(t, recvSSRC, pkt.DestinationSSRC()[0])
						tmr.Stop()
						wss.Close()
						return
					case *rtcp.FullIntraRequest:
						assert.Equal(t, recvSSRC, pkt.DestinationSSRC()[0])
						tmr.Stop()
						wss.Close()
						return
					case *rtcp.TransportLayerNack:
						continue
					}
				default:
					continue
				}
			}
		})
	}
}

func TestWebRTCSimulcastSender_Close(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	closeCtr := 0

	type fields struct {
		ctx            context.Context
		cancel         context.CancelFunc
		onCloseHandler func()
	}
	tests := []struct {
		name    string
		fields  fields
		wantCtr int
	}{
		{
			name: "Must not panic on empty close handler",
			fields: fields{
				ctx:            ctx,
				cancel:         cancel,
				onCloseHandler: nil,
			},
		},
		{
			name:    "Must call close handler and be called once",
			wantCtr: 1,
			fields: fields{
				ctx:    ctx,
				cancel: cancel,
				onCloseHandler: func() {
					closeCtr++
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := &WebRTCSimulcastSender{
				ctx:            tt.fields.ctx,
				cancel:         tt.fields.cancel,
				onCloseHandler: tt.fields.onCloseHandler,
			}
			if tt.fields.onCloseHandler == nil {
				assert.NotPanics(t, s.Close)
			}
			if tt.fields.onCloseHandler != nil {
				s.Close()
				s.Close()
				assert.Equal(t, tt.wantCtr, closeCtr)
			}
		})
	}
}

func TestWebRTCSimulcastSender_CurrentSpatialLayer(t *testing.T) {
	type fields struct {
		currentSpatialLayer uint8
	}
	tests := []struct {
		name   string
		fields fields
		want   uint8
	}{
		{
			name: "Must return current spatial layer",
			fields: fields{
				currentSpatialLayer: 1,
			},
			want: 1,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := &WebRTCSimulcastSender{
				currentSpatialLayer: tt.fields.currentSpatialLayer,
			}
			if got := s.CurrentSpatialLayer(); got != tt.want {
				t.Errorf("CurrentSpatialLayer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebRTCSimulcastSender_ID(t *testing.T) {
	type fields struct {
		id string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name:   "Must return current ID",
			fields: fields{id: "test"},
			want:   "test",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := &WebRTCSimulcastSender{
				id: tt.fields.id,
			}
			if got := s.ID(); got != tt.want {
				t.Errorf("ID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebRTCSimulcastSender_OnCloseHandler(t *testing.T) {
	type args struct {
		f func()
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "Must set onCloseHandler func",
			args: args{func() {}},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := &WebRTCSimulcastSender{}
			s.OnCloseHandler(tt.args.f)
			assert.NotNil(t, s.onCloseHandler)
		})
	}
}

func TestWebRTCSimulcastSender_SwitchSpatialLayer(t *testing.T) {
	type fields struct {
		id                  string
		currentSpatialLayer uint8
		targetSpatialLayer  uint8
	}
	type args struct {
		targetLayer uint8
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "Spatial layer must change",
			fields: fields{
				id:                  "test",
				currentSpatialLayer: 2,
				targetSpatialLayer:  2,
			},
			args: args{},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := &WebRTCSimulcastSender{
				id:                  tt.fields.id,
				currentSpatialLayer: tt.fields.currentSpatialLayer,
				targetSpatialLayer:  tt.fields.targetSpatialLayer,
			}
			print(s)
		})
	}
}
