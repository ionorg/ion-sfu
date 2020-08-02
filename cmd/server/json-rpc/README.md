# json-rpc

`ion-sfu` supports a `json-rpc` interface for connecting peers to the sfu

## Quick Start
### Serving over http
```
go build cmd/server/json-rpc/main.go
./main -c config.toml -p 7000
```

### Serving over `https`
Generate a keypair and run:
```
go build cmd/server/json-rpc/main.go
./main -c config.toml -key ./key.pem -cert ./cert.pem -a "0.0.0.0:10000"
```

## API

TODO