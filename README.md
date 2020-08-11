<h1 align="center">
  <br>
  Ion SFU
  <br>
</h1>
<h4 align="center">Go implementation of a WebRTC SFU</h4>
<p align="center">
  <a href="https://pion.ly/slack"><img src="https://img.shields.io/badge/join-us%20on%20slack-gray.svg?longCache=true&logo=slack&colorB=brightgreen" alt="Slack Widget"></a>
  <a href="https://travis-ci.org/pion/ion-sfu"><img src="https://travis-ci.org/pion/ion-sfu.svg?branch=master" alt="Build Status"></a>
  <a href="https://pkg.go.dev/github.com/pion/ion-sfu"><img src="https://godoc.org/github.com/pion/ion-sfu?status.svg" alt="GoDoc"></a>
  <a href="https://codecov.io/gh/pion/ion-sfu"><img src="https://codecov.io/gh/pion/ion-sfu/branch/master/graph/badge.svg" alt="Coverage Status"></a>
  <a href="https://goreportcard.com/report/github.com/pion/ion-sfu"><img src="https://goreportcard.com/badge/github.com/pion/ion-sfu" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
</p>
<br>

Ion-sfu is a high performance WebRTC SFU microservice implemented in Go. It can be called directly or through a [gRPC](cmd/server/grpc) or [json-rpc](cmd/server/json-rpc) interface.

## Getting Started

### Running the grpc server

If you have a local golang environment already setup, simply run

```
go build ./cmd/server/grpc/main.go && ./main -c config.toml
```

If you prefer a containerized environment, you can use the included Docker image

```
docker run -p 50051:50051 -p 5000-5020:5000-5020/udp pion/ion-sfu:v1.0.0-grpc
```

### Interacting with the server

To get an idea of how to interact with the ion-sfu instance, check out our [examples](examples).

### License

MIT License - see [LICENSE](LICENSE) for full text
