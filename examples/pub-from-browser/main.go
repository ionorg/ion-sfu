package main

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/pion/ion-sfu/examples/internal/signal"
	"github.com/pion/ion-sfu/pkg/proto/sfu"
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

	pubOffer := sfu.SessionDescription{}
	signal.Decode(signal.MustReadStdin(), &pubOffer)

	if err != nil {
		log.Fatalf("error decoding pub offer")
	}

	ctx := context.Background()
	stream, err := c.Publish(ctx, &sfu.PublishRequest{
		Rid: "default",
		Options: &sfu.PublishOptions{
			Codec: "VP8",
		},
		Description: &pubOffer,
	})

	if err != nil {
		log.Fatalf("Error publishing stream: %v", err)
	}

	answer, err := stream.Recv()
	if err != nil {
		log.Fatalf("Error receving publish response: %v", err)
	}

	// Output the mid and answer in base64 so we can paste it in browser
	fmt.Printf("\npub mid: %s", answer.Mediainfo.Mid)
	fmt.Printf("\npub answer: %s", signal.Encode(answer.Description))

	answer, err = stream.Recv()
	if err == io.EOF {
		// WebRTC Transport closed
		fmt.Println("WebRTC Transport Closed")
	}

	if err != nil {
		log.Fatalf("Error receving publish response: %v", err)
	}
}
