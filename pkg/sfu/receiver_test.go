package sfu

import (
	"fmt"
	"math"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gammazero/workerpool"
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

func BenchmarkWriteRTP(b *testing.B) {
	cases := []int{1, 10, 100, 500}
	workers := runtime.NumCPU()
	wp := workerpool.New(workers)
	for _, c := range cases {
		// fills each bucket with a max of 50, i.e. []int{50, 50} for c=100
		fill := make([]int, 0)
		for i := 50; ; i += 50 {
			if i == c {
				break
			} else if i > c {
				fill = append(fill, c%50)
				break
			} else {
				fill = append(fill, 50)
			}
		}

		// splits c into numCPU buckets, i.e. []int{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 1} for 12 cpus and c=100
		split := make([]int, workers)
		batch := int(math.Ceil(float64(c) / float64(workers)))
		total := 0
		for i := 0; i < workers; i++ {
			if total+batch > c {
				split[i] = c - total
			} else {
				split[i] = batch
				total += batch
			}
		}

		b.Run(fmt.Sprintf("%d-Downtracks/Control", c), func(b *testing.B) {
			benchmarkNoPool(b, c)
		})
		b.Run(fmt.Sprintf("%d-Downtracks/Fill", c), func(b *testing.B) {
			benchmarkPool(b, wp, fill)
		})
		b.Run(fmt.Sprintf("%d-Downtracks/Split", c), func(b *testing.B) {
			benchmarkPool(b, wp, split)
		})
	}
}

func benchmarkNoPool(b *testing.B, downTracks int) {
	for i := 0; i < b.N; i++ {
		readBuffer()
		for dt := 0; dt < downTracks; dt++ {
			writeRTP()
		}
	}
}

func benchmarkPool(b *testing.B, wp *workerpool.WorkerPool, buckets []int) {
	for i := 0; i < b.N; i++ {
		readBuffer()
		var wg sync.WaitGroup
		for j := range buckets {
			downTracks := buckets[j]
			if downTracks == 0 {
				continue
			}
			wg.Add(1)
			wp.Submit(func() {
				defer wg.Done()
				for dt := 0; dt < downTracks; dt++ {
					writeRTP()
				}
			})
		}
		wg.Wait()
	}
}

func readBuffer() {
	time.Sleep(time.Millisecond * 5)
}

func writeRTP() {
	time.Sleep(time.Microsecond * 50)
}
