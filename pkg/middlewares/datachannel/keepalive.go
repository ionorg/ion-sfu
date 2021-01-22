package datachannel

import (
	"bytes"
	"context"
	"time"

	"github.com/pion/ion-sfu/pkg/sfu"
)

func KeepAlive(timeout time.Duration) func(next sfu.MessageProcessor) sfu.MessageProcessor {
	var timer *time.Timer
	return func(next sfu.MessageProcessor) sfu.MessageProcessor {
		return sfu.ProcessFunc(func(ctx context.Context, args sfu.ProcessArgs) {
			if timer == nil {
				timer = time.AfterFunc(timeout, func() {
					_ = args.Peer.Close()
				})
			}
			if args.Message.IsString && bytes.Equal(args.Message.Data, []byte("ping")) {
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(timeout)
				_ = args.DataChannel.SendText("pong")
				return
			}
			next.Process(ctx, args)
		})
	}
}
