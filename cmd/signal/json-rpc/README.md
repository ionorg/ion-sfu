# json-rpc

`ion-sfu` supports a `json-rpc` interface for connecting peers to the sfu

## Quick Start
### Serving over http
```
go build cmd/signal/json-rpc/main.go
./main -c config.toml -a ":7000"
```

### Serving over `https`
Generate a keypair and run:
```
go build cmd/signal/json-rpc/main.go
./main -c config.toml -key ./key.pem -cert ./cert.pem -a "0.0.0.0:10000"
```

## API

### Join
Initialize a peer connection and join a session.
```json
{
    "sid": "defaultroom",
    "offer": {
        "type": "offer",
        "sdp": "..."
    }
}
```

### Offer
Offer a new sdp to the sfu. Called to renegotiate the peer connection, typically when tracks are added/removed.
```json
{
    "desc": {
        "type": "offer",
        "sdp": "..."
    }
}
```


### Answer
Answer a remote offer from the sfu. Called in response to a remote renegotiation. Typically when new tracks are added to/removed from other peers.
```json
{
    "desc": {
        "type": "answer",
        "sdp": "..."
    }
}
```

### Trickle
Provide an ICE candidate.
```json
{
    "candidate": "..."
}
```
