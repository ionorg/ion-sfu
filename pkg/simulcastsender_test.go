package sfu

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
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
		router Router
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
				ctx: ctx,
				id:  "test",
				router: &RouterMock{
					ConfigFunc: func() RouterConfig {
						return RouterConfig{}
					},
				},
				sender: sender,
				layer:  2,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := NewSimulcastSender(tt.args.ctx, tt.args.id, tt.args.router, tt.args.sender, tt.args.layer)
			assert.NotNil(t, got)
		})
	}
}

func TestSimulcastSender_WriteRTP(t *testing.T) {
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
	}

	fakeRouter := &RouterMock{
		GetReceiverFunc: func(_ uint8) Receiver {
			return fakeReceiver
		},
		SendRTCPFunc: func(pkts []rtcp.Packet) error {
			for _, pkt := range pkts {
				if _, ok := pkt.(*rtcp.PictureLossIndication); ok {
					gotPli <- struct{}{}
				}
			}
			return nil
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
				s := &SimulcastSender{
					ctx:           context.Background(),
					enabled:       atomicBool{1},
					router:        fakeRouter,
					track:         senderTrack,
					simulcastSSRC: simulcastSSRC,
				}
				tmr := time.NewTimer(100 * time.Millisecond)
				s.WriteRTP(tt.fields.packet)
			testLoop:
				for {
					select {
					case <-tmr.C:
						t.Fatal("PLI packet not received")
					case <-gotPli:
						tmr.Stop()
						break testLoop
					}
				}
			}
			if tt.fields.checkPacket {
				s := &SimulcastSender{
					ctx:           context.Background(),
					enabled:       atomicBool{1},
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
	_ = sfu.Close()
	_ = remote.Close()
}

func TestSimulcastSender_receiveRTCP(t *testing.T) {
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
		DeleteSenderFunc: func(_ string) {
		},
	}

	fakeRouter := &RouterMock{
		GetReceiverFunc: func(_ uint8) Receiver {
			return fakeReceiver
		},
		SendRTCPFunc: func(pkts []rtcp.Packet) error {
			for _, pkt := range pkts {
				gotRTCP <- pkt
			}
			return nil
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
			wss := &SimulcastSender{
				ctx:           ctx,
				cancel:        cancel,
				enabled:       atomicBool{1},
				router:        fakeRouter,
				sender:        s,
				track:         senderTrack,
				lSSRC:         recvSSRC,
				simulcastSSRC: simulcastSSRC,
			}
			go wss.receiveRTCP()
			tmr := time.NewTimer(1000 * time.Millisecond)
		testLoop:
			for {
				select {
				case <-tmr.C:
					t.Fatal("RTCP packet not received")
				case <-time.After(10 * time.Millisecond):
					err = remote.WriteRTCP([]rtcp.Packet{tt.want, tt.want, tt.want, tt.want})
					assert.NoError(t, err)
				case pkt := <-gotRTCP:
					switch pkt.(type) {
					case *rtcp.PictureLossIndication:
						assert.Equal(t, recvSSRC, pkt.DestinationSSRC()[0])
						tmr.Stop()
						wss.Close()
						break testLoop
					case *rtcp.FullIntraRequest:
						assert.Equal(t, recvSSRC, pkt.DestinationSSRC()[0])
						tmr.Stop()
						wss.Close()
						break testLoop
					case *rtcp.TransportLayerNack:
						continue
					}
				}
			}
		})
	}
	_ = sfu.Close()
	_ = remote.Close()
}

func TestSimulcastSender_Close(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	closeCtr := 0
	fakeRouter := &RouterMock{
		GetReceiverFunc: func(_ uint8) Receiver {
			return nil
		},
	}

	type fields struct {
		ctx            context.Context
		cancel         context.CancelFunc
		router         Router
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
				router:         fakeRouter,
				onCloseHandler: nil,
			},
		},
		{
			name:    "Must call close handler and be called once",
			wantCtr: 1,
			fields: fields{
				ctx:    ctx,
				cancel: cancel,
				router: fakeRouter,
				onCloseHandler: func() {
					closeCtr++
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := &SimulcastSender{
				ctx:            tt.fields.ctx,
				cancel:         tt.fields.cancel,
				router:         tt.fields.router,
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

func TestSimulcastSender_CurrentSpatialLayer(t *testing.T) {
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
			s := &SimulcastSender{
				currentSpatialLayer: tt.fields.currentSpatialLayer,
			}
			if got := s.CurrentSpatialLayer(); got != tt.want {
				t.Errorf("CurrentSpatialLayer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSimulcastSender_ID(t *testing.T) {
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
			s := &SimulcastSender{
				id: tt.fields.id,
			}
			if got := s.ID(); got != tt.want {
				t.Errorf("ID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSimulcastSender_OnCloseHandler(t *testing.T) {
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
			s := &SimulcastSender{}
			s.OnCloseHandler(tt.args.f)
			assert.NotNil(t, s.onCloseHandler)
		})
	}
}

func TestSimulcastSender_SwitchSpatialLayer(t *testing.T) {
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
			s := &SimulcastSender{
				id:                  tt.fields.id,
				currentSpatialLayer: tt.fields.currentSpatialLayer,
				targetSpatialLayer:  tt.fields.targetSpatialLayer,
			}
			print(s)
		})
	}
}

func TestSimulcastSender_Mute(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	var remoteTrack *webrtc.Track
	gotTrack := make(chan struct{}, 1)

	remote.OnTrack(func(track *webrtc.Track, _ *webrtc.RTPReceiver) {
		_, err := track.ReadRTP()
		assert.NoError(t, err)
		remoteTrack = track
		gotTrack <- struct{}{}
	})

	senderTrack, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, 1234, "video", "pion")
	assert.NoError(t, err)
	_, err = sfu.AddTrack(senderTrack)
	assert.NoError(t, err)

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

	gotPli := make(chan struct{}, 1)
	fakeRecv := &ReceiverMock{
		TrackFunc: func() *webrtc.Track {
			return senderTrack
		},
	}

	fakeRouter := &RouterMock{
		GetReceiverFunc: func(_ uint8) Receiver {
			return fakeRecv
		},
		SendRTCPFunc: func(pkts []rtcp.Packet) error {
			for _, pkt := range pkts {
				if _, ok := pkt.(*rtcp.PictureLossIndication); ok {
					gotPli <- struct{}{}
				}
			}
			return nil
		},
	}

	simpleSdr := SimulcastSender{
		ctx:           context.Background(),
		enabled:       atomicBool{1},
		simulcastSSRC: 1234,
		router:        fakeRouter,
		track:         senderTrack,
		payload:       senderTrack.PayloadType(),
		lSSRC:         1234,
	}
	// Simple sender must forward packets while the sender is not muted
	fakePkt := senderTrack.Packetizer().Packetize([]byte{0x05, 0x06, 0x07, 0x08}, 1)[0]
	simpleSdr.WriteRTP(fakePkt)
	packet, err := remoteTrack.ReadRTP()
	assert.NoError(t, err)
	assert.Equal(t, fakePkt.Payload, packet.Payload)
	// Mute track will prevent tracks to reach the subscriber
	simpleSdr.Mute(true)
	for i := 0; i <= 5; i++ {
		simpleSdr.WriteRTP(senderTrack.Packetizer().Packetize([]byte{0x05, 0x06, 0x07, 0x08}, 1)[0])
	}
	simpleSdr.Mute(false)
	// Now that is un-muted sender will request a key frame
	simpleSdr.WriteRTP(senderTrack.Packetizer().Packetize([]byte{0x05, 0x06, 0x07, 0x08}, 1)[0])
	<-gotPli
	// Write VP8 keyframe
	keyFramePkt := senderTrack.Packetizer().Packetize([]byte{0x05, 0x06, 0x07, 0x08}, 1)[0]
	keyFramePkt.Payload = []byte{0xff, 0xff, 0xff, 0xfd, 0xb4, 0x9f, 0x94, 0x1}
	simpleSdr.WriteRTP(keyFramePkt)
	packet2, err := remoteTrack.ReadRTP()
	assert.NoError(t, err)
	assert.Equal(t, keyFramePkt.Payload, packet2.Payload)
	// Packet 1 and packet 2 must have contiguous SN even after skipped packets while muted
	assert.Equal(t, packet.SequenceNumber+1, packet2.SequenceNumber)
}
