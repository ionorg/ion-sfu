package datachannel

import (
	"bytes"
	"time"

	"github.com/pion/ion-sfu/pkg/sfu"
	"github.com/pion/webrtc/v3"
)

func KeepAlive(timeout time.Duration) func(next sfu.MessageProcessor) sfu.MessageProcessor {
	var timer *time.Timer
	return func(next sfu.MessageProcessor) sfu.MessageProcessor {
		return sfu.ProcessFunc(func(peer *sfu.Peer, dc *webrtc.DataChannel, msg webrtc.DataChannelMessage) {
			if timer == nil {
				timer = time.AfterFunc(timeout, func() {
					_ = peer.Close()
				})
			}
			if msg.IsString && bytes.Equal(msg.Data, []byte("ping")) {
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(timeout)
				_ = dc.SendText("pong")
				return
			}
			next.Process(peer, dc, msg)
		})
	}
}
