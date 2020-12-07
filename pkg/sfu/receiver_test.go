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
