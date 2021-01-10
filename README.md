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
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
</p>
<p align="center">
  <a href="#quickstart">Quickstart</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="./docs">Docs</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="./examples">Examples</a>
</p>
<br>

A [selective forwarding unit](https://webrtcglossary.com/sfu/) is a video routing service which allows webrtc sessions to scale more efficiently. This package provides a simple, flexible, high performance Go implementation of a WebRTC SFU. It can be called directly or through a [gRPC](cmd/signal/grpc) or [json-rpc](cmd/signal/json-rpc) interface.

## Features

- Audio/Video/Datachannel Support
- Congestion Control (TWCC, REMB, RR/SR)
- Unified plan semantics
- Simulcast spatial and temporal layer switching
- Embedded TURN server
- Prometheus metrics

## Getting Started

Get up and running in minutes with the **Echo Test** example:

```
docker-compose -f examples/echotest/docker-compose.yaml up
```

Open the client:

```
http://localhost:8000/
```

Checkout our [documentation](./docs) for more info!

## Contributing

We welcome contributions! Feel free to open an issue or pull request.

## License

MIT License - see [LICENSE](LICENSE) for full text
