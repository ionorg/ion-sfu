package sfu

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

func TestNewWebRTCReceiver(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)
	track, err := sfu.NewTrack(webrtc.DefaultPayloadTypeOpus, 1234, "audio", "pion")
	assert.NoError(t, err)

	type args struct {
		ctx    context.Context
		track  *webrtc.Track
		config RouterConfig
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "Must return a non nil Receiver",
			args: args{
				ctx:    context.Background(),
				track:  track,
				config: RouterConfig{},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := NewWebRTCReceiver(tt.args.ctx, tt.args.track, tt.args.config)
			assert.NotNil(t, got)
		})
	}
}

func TestWebRTCReceiver_AddSender(t *testing.T) {
	type fields struct {
		senders map[string]Sender
	}
	type args struct {
		sender Sender
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int
	}{
		{
			name: "Must add new sender to receiver",
			fields: fields{
				senders: make(map[string]Sender),
			},
			args: args{sender: &SenderMock{
				IDFunc: func() string {
					return "test"
				},
			}},
			want: 1,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &WebRTCReceiver{
				senders: tt.fields.senders,
			}
			w.AddSender(tt.args.sender)
			assert.Equal(t, tt.want, len(w.senders))
		})
	}
}

func TestWebRTCReceiver_Close(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	type fields struct {
		ctx    context.Context
		cancel context.CancelFunc
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			name: "Must cancel context",
			fields: fields{
				ctx:    ctx,
				cancel: cancel,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &WebRTCReceiver{
				ctx:    tt.fields.ctx,
				cancel: tt.fields.cancel,
			}
			w.Close()
			assert.Error(t, ctx.Err())
		})
	}
}

func TestWebRTCReceiver_DeleteSender(t *testing.T) {
	type fields struct {
		senders map[string]Sender
	}
	type args struct {
		pid string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int
	}{
		{
			name: "Must delete a sender by pID",
			fields: fields{
				senders: map[string]Sender{"test": &SenderMock{}},
			},
			args: args{pid: "test"},
			want: 0,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &WebRTCReceiver{
				senders: tt.fields.senders,
			}
			w.DeleteSender(tt.args.pid)
			assert.Equal(t, tt.want, len(w.senders))
		})
	}
}

func TestWebRTCReceiver_OnCloseHandler(t *testing.T) {
	type args struct {
		fn func()
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "Must set on close handler function",
			args: args{
				fn: func() {},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &WebRTCReceiver{}
			w.OnCloseHandler(tt.args.fn)
			assert.NotNil(t, w.onCloseHandler)
		})
	}
}

func TestWebRTCReceiver_ReadRTCP(t *testing.T) {
	rtcpChan := make(chan rtcp.Packet, 5)
	type fields struct {
		rtcpCh chan rtcp.Packet
	}
	tests := []struct {
		name   string
		fields fields
		want   chan rtcp.Packet
	}{
		{
			name: "Must return rtcp chan",
			fields: fields{
				rtcpCh: rtcpChan,
			},
			want: rtcpChan,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &WebRTCReceiver{
				rtcpCh: tt.fields.rtcpCh,
			}
			if got := w.ReadRTCP(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ReadRTCP() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebRTCReceiver_SpatialLayer(t *testing.T) {
	type fields struct {
		spatialLayer uint8
	}
	tests := []struct {
		name   string
		fields fields
		want   uint8
	}{
		{
			name: "Must return current spatial layer",
			fields: fields{
				spatialLayer: 1,
			},
			want: 1,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &WebRTCReceiver{
				spatialLayer: tt.fields.spatialLayer,
			}
			if got := w.SpatialLayer(); got != tt.want {
				t.Errorf("SpatialLayer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebRTCReceiver_Track(t *testing.T) {
	me := webrtc.MediaEngine{}
	me.RegisterDefaultCodecs()
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	sfu, err := api.NewPeerConnection(webrtc.Configuration{})
	assert.NoError(t, err)
	track, err := sfu.NewTrack(webrtc.DefaultPayloadTypeOpus, 1234, "audio", "pion")
	assert.NoError(t, err)

	type fields struct {
		track *webrtc.Track
	}
	tests := []struct {
		name   string
		fields fields
		want   *webrtc.Track
	}{
		{
			name: "Must return current track",
			fields: fields{
				track: track,
			},
			want: track,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &WebRTCReceiver{
				track: tt.fields.track,
			}
			if got := w.Track(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Track() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebRTCReceiver_WriteRTCP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ctxCanceled, cc := context.WithCancel(context.Background())
	cc()
	type fields struct {
		ctx    context.Context
		cancel context.CancelFunc
		rtcpCh chan rtcp.Packet
	}
	type args struct {
		pkt rtcp.Packet
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			name: "Must write rtcp in channel",
			fields: fields{
				ctx:    ctx,
				cancel: cancel,
				rtcpCh: make(chan rtcp.Packet, 5),
			},
			args: args{
				pkt: &rtcp.PictureLossIndication{},
			},
			wantErr: false,
		},
		{
			name: "Must return error if channel is nil or ctx canceled",
			fields: fields{
				ctx:    ctxCanceled,
				cancel: cc,
				rtcpCh: nil,
			},
			args: args{
				pkt: &rtcp.PictureLossIndication{},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &WebRTCReceiver{
				ctx:    tt.fields.ctx,
				cancel: tt.fields.cancel,
				rtcpCh: tt.fields.rtcpCh,
			}
			err := w.WriteRTCP(tt.args.pkt)
			if (err != nil) != tt.wantErr {
				t.Errorf("WriteRTCP() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				assert.Equal(t, 1, len(tt.fields.rtcpCh))
			}
		})
	}
}

func TestWebRTCReceiver_fwdRTP(t *testing.T) {
	type fields struct {
		rtpCh   chan *rtp.Packet
		senders map[string]Sender
	}

	var ctr int64
	fakeSender := SenderMock{
		WriteRTPFunc: func(_ *rtp.Packet) {
			atomic.AddInt64(&ctr, 1)
		},
	}

	tests := []struct {
		name   string
		fields fields
		want   int
	}{
		{
			name: "Receiver must fwd the pkts to every sender",
			fields: fields{
				rtpCh:   make(chan *rtp.Packet),
				senders: map[string]Sender{"test": &fakeSender},
			},
			want: 10,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &WebRTCReceiver{
				senders: tt.fields.senders,
				rtpCh:   tt.fields.rtpCh,
			}
			go w.fwdRTP()
			for i := 0; i < tt.want; i++ {
				w.rtpCh <- &rtp.Packet{}
			}
			time.Sleep(50 * time.Millisecond)
			refCtr := atomic.LoadInt64(&ctr)
			assert.Equal(t, int64(tt.want), refCtr)
			close(w.rtpCh)
		})
	}
}

func TestWebRTCReceiver_closeSenders(t *testing.T) {
	closeChan := make(chan struct{}, 1)

	fakeSender := SenderMock{
		CloseFunc: func() {
			closeChan <- struct{}{}
		},
	}

	type fields struct {
		senders map[string]Sender
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			name: "Must call close methods on senders",
			fields: fields{
				senders: map[string]Sender{"test": &fakeSender},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			w := &WebRTCReceiver{
				senders: tt.fields.senders,
			}
			w.closeSenders()
			tmr := time.NewTimer(100 * time.Millisecond)
			for {
				select {
				case <-tmr.C:
					t.Fatal("No close method called")
				case <-closeChan:
					tmr.Stop()
					return
				}

			}
		})
	}
}
