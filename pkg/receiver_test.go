package sfu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
