# Datachannels middlewares

`ion-sfu` supports datachannels middlewares similar to the `net/http` standard library handlers. 

## API

### Middleware

To create a datachannel middleware, just follow below pattern:

```go
func SubscriberAPI(next sfu.MessageProcessor) sfu.MessageProcessor {
	return sfu.ProcessFunc(func(ctx context.Context, args sfu.ProcessArgs) {
              next.Process(ctx,args)
	}
}
```

### Init middlewares

To initialize the middlewares you need to declare them after sfu initialization:
```go
s := sfu.NewSFU(conf)
dc := s.NewDatachannel(sfu.APIChannelLabel)
dc.Use(datachannel.KeepAlive(5*time.Second), datachannel.SubscriberAPI)
// This callback is optional
dc.OnMessage(func(ctx context.Context, msg webrtc.DataChannelMessage, in *webrtc.DataChannel, out []*webrtc.DataChannel) {
})
```
The datachannels will be negotiated on peer join in the `Subscriber` peer connection.