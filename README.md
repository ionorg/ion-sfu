<h1 align="center">
  <br>
  Ion SFU
  <br>
</h1>
<h4 align="center">Go implementation of a WebRTC SFU</h4>
<p align="center">
  <a href="http://gophers.slack.com/messages/pion"><img src="https://img.shields.io/badge/join-us%20on%20slack-gray.svg?longCache=true&logo=slack&colorB=brightgreen" alt="Slack Widget"></a>
  <a href="https://travis-ci.org/pion/ion-sfu"><img src="https://travis-ci.org/pion/ion-sfu.svg?branch=master" alt="Build Status"></a>
  <a href="https://pkg.go.dev/github.com/pion/ion-sfu"><img src="https://godoc.org/github.com/pion/ion-sfu?status.svg" alt="GoDoc"></a>
  <a href="https://codecov.io/gh/pion/ion-sfu"><img src="https://codecov.io/gh/pion/ion-sfu/branch/master/graph/badge.svg" alt="Coverage Status"></a>
  <a href="https://goreportcard.com/report/github.com/pion/ion-sfu"><img src="https://goreportcard.com/badge/github.com/pion/ion-sfu" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
</p>
<br>

Ion sfu is a high performance WebRTC SFU microservice implemented in Go. It exposes a gRPC interface and can be easily integrated into existing systems.

## Getting Started

The fastest way to get started is to use the included Docker environment.

```
docker build -t pion/ion-sfu .
docker run -p 50051:50051 -p 5000-5020:5000-5020/udp pion/ion-sfu:latest
```

Publishing a stream to the sfu:

```go
package main

import (
	"context"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"github.com/pion/ion-sfu/pkg/proto/sfu"
)

const (
	address = "localhost:50051"
)

func main() {
	// Set up a connection to the server.
	conn, err := grpc.Dial(address, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := sfu.NewSFUClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := c.Publish(ctx, &sfu.PublishRequest{
        Uid: "xxxx",
        rid: "",
        Options: &sfu.PublishOptions{...},
        Description: &sfu.SessionDescription{...}
    })
```

## Contributors

<a href="https://github.com/pion/ion-sfu/graphs/contributors"><img src="https://opencollective.com/pion-ion-sfu/contributors.svg?width=890&button=false" /></a>

### License

MIT License - see [LICENSE](LICENSE) for full text
