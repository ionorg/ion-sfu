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

	offer := sfu.SessionDescription{}
	signal.Decode(signal.MustReadStdin(), &offer)

	if err != nil {
		log.Fatalf("error decoding offer")
	}

	stream, err := c.Publish(context.Background(), &sfu.PublishRequest{
		Uid: "userid",
		Rid: "default",
		Options: &sfu.PublishOptions{
			Codec: "VP8",
		},
		Description: &offer,
	})

	if err != nil {
		log.Fatalf("Error publishing stream: %v", err)
	}

	for {
		answer, err := stream.Recv()
		if err == io.EOF {
			// WebRTC Transport closed
			fmt.Println("WebRTC Transport Closed")
			break
		}

		if err != nil {
			log.Fatalf("Error receving publish response: %v", err)
		}

		// Output the answer in base64 so we can paste it in browser
		fmt.Println(signal.Encode(answer.Description))
	}
}
