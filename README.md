<h1 align="center">
  <br>
  Ion SFU
  <br>
</h1>
<h4 align="center">Go implementation of a WebRTC Selective Forwarding Unit</h4>
<p align="center">
  <a href="https://pion.ly/slack"><img src="https://img.shields.io/badge/join-us%20on%20slack-gray.svg?longCache=true&logo=slack&colorB=brightgreen" alt="Slack Widget"></a>
  <a href="https://pkg.go.dev/github.com/pion/ion-sfu"><img src="https://godoc.org/github.com/pion/ion-sfu?status.svg" alt="GoDoc"></a>
  <a href="https://codecov.io/gh/pion/ion-sfu"><img src="https://codecov.io/gh/pion/ion-sfu/branch/master/graph/badge.svg" alt="Coverage Status"></a>
  <a href="https://goreportcard.com/report/github.com/pion/ion-sfu"><img src="https://goreportcard.com/badge/github.com/pion/ion-sfu" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
</p>
<br>

A [selective forwarding unit](https://webrtcglossary.com/sfu/) is a video routing service which allows webrtc sessions to scale more efficiently. This package provides a simple, flexible, high performance Go implementation of a WebRTC SFU. It can be called directly or through a [gRPC](cmd/signal/grpc) or [json-rpc](cmd/signal/json-rpc) interface.

## Features
* Audio/Video/Datachannel forwarding
* Congestion Control (TWCC, REMB, RR/SR)
* Unified plan semantics
* Pub/Sub Peer Connection (`O(n)` port usage)
* Audio level indication (RFC6464). "X is speaking"

## Quickstart

Run the Echo Test example

```
docker-compose -f examples/echotest/docker-compose.yaml up
```

Open the client
```
http://localhost:8000/
```

### SFU with json-rpc signaling

The json-rpc signaling service can be used to easily get up and running with the sfu. It can be used with the [corresponding javascript signaling module](https://github.com/pion/ion-sdk-js/blob/master/src/signal/ion-sfu.ts).

##### Using golang environment

```
go build ./cmd/signal/json-rpc/main.go && ./main -c config.toml
```

##### Using docker

```
docker run -p 7000:7000 -p 5000-5200:5000-5200/udp pionwebrtc/ion-sfu:latest-jsonrpc
```

### SFU with gRPC signaling

For service-to-service communication, you can use the grpc interface. A common pattern is to call the grpc endpoints from a custom signaling service.

##### Using golang environment

```
go build ./cmd/signal/grpc/main.go && ./main -c config.toml
```

##### Using docker

```
docker run -p 50051:50051 -p 5000-5200:5000-5200/udp pionwebrtc/ion-sfu:latest-grpc
```

## Documentation

Answers to some [Frequenty Asked Questions](FAQ.md).

## Examples

To see some other ways of interacting with the ion-sfu instance, check out our [examples](examples).

## Media Processing

`ion-sfu` supports real-time processing on media streamed through the sfu using [`ion-avp`](https://github.com/pion/ion-avp).

For an example of recording a MediaStream to webm, checkout the [save-to-webm](https://github.com/pion/ion-avp/tree/master/examples/save-to-webm) example.

### License

MIT License - see [LICENSE](LICENSE) for full text

## Development

Generate the protocol buffers and grpc code:
 1. Best choice (uses docker): `make protos`.
 2. Manually:
     - Install protocol buffers and the protcol buffers compiler. On Fedora `dnf install protobuf protobuf-compiler`.
     - `go get google.golang.org/grpc/cmd/protoc-gen-go-grpc`
     - `go get google.golang.org/protobuf/cmd/protoc-gen-go`
     - `protoc --go_out=. --go-grpc_out=. --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative cmd/signal/grpc/proto/sfu.proto`
