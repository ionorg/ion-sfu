<h1 align="center">
  <br>
  Ion SFU
  <br>
</h1>
<h4 align="center">Go implementation of a WebRTC Selective Forwarding Unit</h4>
<p align="center">
  <a href="https://pion.ly/slack"><img src="https://img.shields.io/badge/join-us%20on%20slack-gray.svg?longCache=true&logo=slack&colorB=brightgreen" alt="Slack Widget"></a>
  <a href="https://travis-ci.com/pion/ion-sfu"><img src="https://travis-ci.com/pion/ion-sfu.svg?branch=master" alt="Build Status"></a>
  <a href="https://pkg.go.dev/github.com/pion/ion-sfu"><img src="https://godoc.org/github.com/pion/ion-sfu?status.svg" alt="GoDoc"></a>
  <a href="https://codecov.io/gh/pion/ion-sfu"><img src="https://codecov.io/gh/pion/ion-sfu/branch/master/graph/badge.svg" alt="Coverage Status"></a>
  <a href="https://goreportcard.com/report/github.com/pion/ion-sfu"><img src="https://goreportcard.com/badge/github.com/pion/ion-sfu" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
</p>
<br>

A [selective forwarding unit](https://webrtcglossary.com/sfu/) is a video routing service which allows webrtc sessions to scale more efficiently. This package provides a simple, flexible, high performance Go implementation of a WebRTC SFU. It can be called directly or through a [gRPC](cmd/signal/grpc) or [json-rpc](cmd/signal/json-rpc) interface.

## Getting Started

### Running the json-rpc signaling server

If you have a local golang environment already setup, simply run

```
go build ./cmd/signal/json-rpc/main.go && ./main -c config.toml
```

If you prefer a containerized environment, you can use the included Docker image

```
docker run -p 50051:50051 -p 5000-5020:5000-5020/udp pionwebrtc/ion-sfu:latest-jsonrpc
```

### Running the grpc signaling server

If you have a local golang environment already setup, simply run

```
go build ./cmd/signal/grpc/main.go && ./main -c config.toml
```

If you prefer a containerized environment, you can use the included Docker image

```
docker run -p 50051:50051 -p 5000-5020:5000-5020/udp pionwebrtc/ion-sfu:latest-grpc
```

### Interacting with the server

To get an idea of how to interact with the ion-sfu instance, check out our [examples](examples).

### Processing Media

`ion-sfu` supports real-time processing on media streamed through the sfu using [`ion-avp`](https://github.com/pion/ion-avp).

For an example of recording a MediaStream to webm, checkout the [save-to-webm](https://github.com/pion/ion-avp/tree/master/examples/save-to-webm) example.

### License

MIT License - see [LICENSE](LICENSE) for full text
