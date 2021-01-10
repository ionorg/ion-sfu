# Using JSON-RPC

## Signaling

The json-rpc signaling service can be used to easily get up and running with the sfu. It can be used with the [corresponding javascript signaling module](https://github.com/pion/ion-sdk-js/blob/master/src/signal/ion-sfu.ts).

### Golang environment

```
go build ./cmd/signal/json-rpc/main.go && ./main -c config.toml
```

### Docker

```
docker run -p 7000:7000 -p 5000-5200:5000-5200/udp pionwebrtc/ion-sfu:latest-jsonrpc
```
