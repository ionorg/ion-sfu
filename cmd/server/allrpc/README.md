# allrpc

allrpc supports both `grpc` and `json-rpc` interface for connecting peers to the sfu
you can choose `grpc` or `json-rpc` or both

## Quick Start
```
go build -o allrpc cmd/server/allrpc/main.go
./allrpc -jaddr ":8000" -gaddr ":7000" -c config.toml
```
