package sfu

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

func TestNewSimpleSender(t *testing.T) {
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
	}
	tests := []struct {
		name string
		args args
		want Sender
	}{
		{
			name: "Must return a non nil Sender",
			args: args{
				ctx:    ctx,
				id:     "test",
				router: nil,
				sender: sender,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := NewSimpleSender(tt.args.ctx, tt.args.id, tt.args.router, tt.args.sender)
			assert.NotNil(t, got)
		})
	}
}

func TestSimpleSender_WriteRTP(t *testing.T) {
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

	fakePktPT := 12
	fakePkt := senderTrack.Packetizer().Packetize([]byte{0x01, 0x02, 0x03, 0x04}, 1)[0]
	fakePkt.PayloadType = uint8(fakePktPT)

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
	}{
		{
			name: "Must write packet to track, with correct PT",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			s := &SimpleSender{
				ctx:     ctx,
				cancel:  cancel,
				enabled: atomicBool{1},
				payload: senderTrack.PayloadType(),
				track:   senderTrack,
			}
			tmr := time.NewTimer(1000 * time.Millisecond)
			s.WriteRTP(fakePkt)
			for {
				pkt, err := remoteTrack.ReadRTP()
				assert.NoError(t, err)
				if pkt.SequenceNumber == fakePkt.SequenceNumber {
					assert.NotEqual(t, fakePkt.PayloadType, pkt.PayloadType)
					assert.Equal(t, senderTrack.Codec().PayloadType, pkt.PayloadType)
					tmr.Stop()
					break
				}
				select {
				case <-tmr.C:
					t.Fatal("packet not received")
				}
			}
		})
	}
	_ = sfu.Close()
	_ = remote.Close()
}

func TestSimpleSender_receiveRTCP(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, remote, err := newPair(webrtc.Configuration{}, api)
	assert.NoError(t, err)

	gotTrack := make(chan struct{}, 1)

	remote.OnTrack(func(track *webrtc.Track, r *webrtc.RTPReceiver) {
		_, err := track.ReadRTP()
		assert.NoError(t, err)
		gotTrack <- struct{}{}
	})

	senderTrack, err := sfu.NewTrack(webrtc.DefaultPayloadTypeVP8, 1234, "video", "pion")
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
			name: "Sender must forward PLI messages",
			want: &rtcp.PictureLossIndication{
				SenderSSRC: 1234,
				MediaSSRC:  1234,
			},
		},
		{
			name: "Sender must forward FIR messages",
			want: &rtcp.FullIntraRequest{
				SenderSSRC: 1234,
				MediaSSRC:  1234,
			},
		},
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			wss := &SimpleSender{
				ctx:    ctx,
				cancel: cancel,
				router: fakeRouter,
				sender: s,
				track:  senderTrack,
			}
			go wss.receiveRTCP()
			tmr := time.NewTimer(5000 * time.Millisecond)
		testLoop:
			for {
				select {
				case <-time.After(20 * time.Millisecond):
					err := remote.WriteRTCP([]rtcp.Packet{tt.want, tt.want, tt.want, tt.want})
					assert.NoError(t, err)
				case <-tmr.C:
					t.Fatal("RTCP packet not received")
				case pkt := <-gotRTCP:
					switch pkt.(type) {
					case *rtcp.PictureLossIndication:
						tmr.Stop()
						wss.Close()
						break testLoop
					case *rtcp.FullIntraRequest:
						tmr.Stop()
						wss.Close()
						break testLoop
					default:
						continue
					}
				}
			}
		})
	}
	_ = sfu.Close()
	_ = remote.Close()
}

func TestSimpleSender_Close(t *testing.T) {
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
			s := &SimpleSender{
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

func TestSimpleSender_CurrentSpatialLayer(t *testing.T) {
	tests := []struct {
		name string
		want uint8
	}{
		{
			name: "Must return zero layer",
			want: 0,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := &SimpleSender{}
			if got := s.CurrentSpatialLayer(); got != tt.want {
				t.Errorf("CurrentSpatialLayer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSimpleSender_ID(t *testing.T) {
	type fields struct {
		id string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name: "Must return correct ID",
			fields: fields{
				id: "test",
			},
			want: "test",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := &SimpleSender{
				id: tt.fields.id,
			}
			if got := s.ID(); got != tt.want {
				t.Errorf("ID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSimpleSender_OnCloseHandler(t *testing.T) {
	type args struct {
		fn func()
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "Must set onCloseHandler func",
			args: args{fn: func() {}},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := &SimpleSender{}
			s.OnCloseHandler(tt.args.fn)
			assert.NotNil(t, s.onCloseHandler)
		})
	}
}

func TestSimpleSender_SwitchSpatialLayer(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "Function not supported in simple sender, just log a warn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &SimpleSender{}
			assert.NotPanics(t, func() {
				s.SwitchSpatialLayer(4)
			})
		})
	}
}

func TestSimpleSender_SwitchTemporalLayer(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "Function not supported in simple sender, just log a warn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &SimpleSender{}
			assert.NotPanics(t, func() {
				s.SwitchSpatialLayer(4)
			})
		})
	}
}

func TestSimpleSender_Mute(t *testing.T) {
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
	fakeRecv := &ReceiverMock{}

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

	simpleSdr := SimpleSender{
		ctx:     context.Background(),
		enabled: atomicBool{1},
		router:  fakeRouter,
		track:   senderTrack,
		payload: senderTrack.PayloadType(),
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
