package sfu

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

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
			go w.writeRTP()
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
