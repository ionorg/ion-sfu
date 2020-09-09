# relay-to-remote
relay-to-remote demonstrates how to relay media with an `ion-sfu`. in this example, the media is relayed from `ion-sfu`, to a remote `ion-sfu`.

## Instructions

### Start ion client
Run `go run examples/relay-to-remote/main.go -sid=$SESSION_ID -sfu=$SFU_REMOTE_ADDRESS`. This will initiate a webrtc transport from sfu to a remote sfu. Tracks will start being relayed.

Congrats, you are now relaying tracks with the ion-sfu to a remote ion-sfu server! Now start building something cool!
