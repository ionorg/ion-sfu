package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/pion/ion-sfu/examples/internal/signal"
	sfu "github.com/pion/ion-sfu/pkg/proto"
	"google.golang.org/grpc"
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

	subOffer := sfu.SessionDescription{}
	signal.Decode(signal.MustReadStdin(), &subOffer)

	mid := os.Args[1]

	ctx := context.Background()
	sub, err := c.Subscribe(ctx, &sfu.SubscribeRequest{Mid: mid, Description: &subOffer})

	if err != nil {
		log.Panicf("Error subscribing: %v", err)
	}

	fmt.Printf("\n\nsub mid: %s", sub.Mid)
	fmt.Printf("\nsub answer: %s\n", signal.Encode(sub.Description))
}
