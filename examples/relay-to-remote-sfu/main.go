// Package relay-to-remote-sfu contains an example of relaying to a remote sfu from
// an ion-sfu instance from a file on disk.
package main

import (
	"context"
	"flag"
	"github.com/google/uuid"
	pb "github.com/pion/ion-sfu/cmd/server/grpc/proto"
	"google.golang.org/grpc"
	"log"
)

const (
	sfuHost = "localhost:50051"
)

func main() {

	// Set up a connection to the sfu server.
	conn, err := grpc.Dial(sfuHost, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewSFUClient(conn)

	sid := flag.String("sid", uuid.New().String(), "session id")
	sfu := flag.String("sfu", "", "sfu remote address")

	flag.Parse()

	if *sfu == "" {
		log.Fatalf("sfu address cannot be empty")
	}

	ctx := context.Background()

	_, err = c.Relay(ctx, &pb.RelayRequest{
		Sfu: *sfu,
		Sid: *sid,
	})

	if err != nil {
		log.Fatalf("Error intializing remote sfu relay stream: %v", err)
	}

	select {}
}
