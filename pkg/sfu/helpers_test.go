package sfu

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/pion/ion-sfu/pkg/buffer"
)

func Test_setVP8TemporalLayer(t *testing.T) {
	packetFactory = &sync.Pool{
		New: func() interface{} {
			return make([]byte, 1460)
		},
	}

	type args struct {
		pl []byte
		dt *DownTrack
	}
	tests := []struct {
		name        string
		args        args
		wantPayload []byte
		wantSkip    bool
	}{
		{
			name: "Must skip when current temporal is bigger than wanted",
			args: args{
				dt: &DownTrack{
					mime: "video/vp8",
					simulcast: simulcastTrackHelpers{
						refPicID: 0,
					},
				},
				pl: []byte{0xff, 0xff, 0xff, 0xfd, 0xb4, 0xdf, 0x5, 0x6},
			},
			wantPayload: nil,
			wantSkip:    true,
		},
		{
			name: "Must return modified payload",
			args: args{
				dt: &DownTrack{
					mime: "video/vp8",
					simulcast: simulcastTrackHelpers{
						refPicID: 32764,
					},
				},
				pl: []byte{0xff, 0xff, 0xff, 0xfd, 0xb4, 0xdf, 0x5, 0x6},
			},
			wantPayload: []byte{0xff, 0xff, 0x80, 0x01, 0x1, 0xdf, 0x5, 0x6},
			wantSkip:    false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			gotPayload, gotSkip := setVP8TemporalLayer(buffer.ExtPacket{}, 1, tt.args.dt)
			if !reflect.DeepEqual(gotPayload, tt.wantPayload) {
				t.Errorf("setVP8TemporalLayer() gotPayload = %v, want %v", gotPayload, tt.wantPayload)
			}
			if gotSkip != tt.wantSkip {
				t.Errorf("setVP8TemporalLayer() gotSkip = %v, want %v", gotSkip, tt.wantSkip)
			}
		})
	}
}

func Test_timeToNtp(t *testing.T) {
	type args struct {
		ns int64
	}
	tests := []struct {
		name    string
		args    args
		wantNTP uint64
	}{
		{
			name: "Must return correct NTP time",
			args: args{
				ns: time.Unix(1602391458, 1234).UnixNano(),
			},
			wantNTP: 16369753560730047667,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			gotNTP := timeToNtp(tt.args.ns)
			if gotNTP != tt.wantNTP {
				t.Errorf("timeToNtp() gotFraction = %v, want %v", gotNTP, tt.wantNTP)
			}
		})
	}
}
