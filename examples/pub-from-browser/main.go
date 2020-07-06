// Package pub-from-browser contains an example of publishing a stream to
// an ion-sfu instance from the browser.
package main

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/pion/ion-sfu/examples/internal/signal"
	sfu "github.com/pion/ion-sfu/pkg/proto"
	"github.com/pion/webrtc/v2"
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

	pubOffer := webrtc.SessionDescription{}
	signal.Decode(signal.MustReadStdin(), &pubOffer)

	if err != nil {
		log.Fatalf("error decoding pub offer")
	}

	ctx := context.Background()
	stream, err := c.Publish(ctx)

	if err != nil {
		log.Fatalf("Error publishing response: %v", err)
	}

	err = stream.Send(&sfu.PublishRequest{
		Rid: "default",
		Payload: &sfu.PublishRequest_Connect{
			Connect: &sfu.Connect{
				Description: &sfu.SessionDescription{
					Type: pubOffer.Type.String(),
					Sdp:  []byte(pubOffer.SDP),
				},
			},
		},
	})

	if err != nil {
		log.Fatalf("Error sending connect request: %v", err)
	}

	for {
		res, err := stream.Recv()
		if err == io.EOF {
			// WebRTC Transport closed
			fmt.Println("WebRTC Transport Closed")
			return
		}

		if err != nil {
			log.Fatalf("Error receiving publish response: %v", err)
		}

		switch payload := res.Payload.(type) {
		case *sfu.PublishReply_Connect:
			// Output the mid and answer in base64 so we can paste it in browser
			fmt.Printf("\npub mid: %s", res.Mid)
			fmt.Printf("\npub answer: %s", signal.Encode(
				webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  string(payload.Connect.Description.Sdp),
				}))
		}
	}
}
