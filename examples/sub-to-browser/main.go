// Package sub-to-browser contains an example of subscribing to a stream from
// an ion-sfu instance in the browser.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"

	sfu "github.com/pion/ion-sfu/cmd/server/grpc/proto"
	"github.com/pion/ion-sfu/examples/internal/signal"
	"github.com/pion/webrtc/v3"
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

	subOffer := webrtc.SessionDescription{}
	signal.Decode(signal.MustReadStdin(), &subOffer)

	sid := os.Args[1]

	ctx := context.Background()
	client, err := c.Signal(ctx)

	if err != nil {
		log.Panicf("Error subscribing: %v", err)
	}

	err = client.Send(&sfu.SignalRequest{
		Payload: &sfu.SignalRequest_Join{
			Join: &sfu.JoinRequest{
				Sid: sid,
				Offer: &sfu.SessionDescription{
					Type: subOffer.Type.String(),
					Sdp:  []byte(subOffer.SDP),
				},
			},
		}})

	if err != nil {
		log.Fatalf("Error sending subscribe request: %v", err)
	}

	for {
		reply, err := client.Recv()
		if err == io.EOF {
			// WebRTC Transport closed
			fmt.Println("WebRTC Transport Closed")
			return
		}

		if err != nil {
			log.Fatalf("Error receiving publish response: %v", err)
		}

		if payload, ok := reply.Payload.(*sfu.SignalReply_Join); ok {
			// Output the mid and answer in base64 so we can paste it in browser
			fmt.Printf("\npid: %s", payload.Join.Pid)
			fmt.Printf("\nsub answer: %s", signal.Encode(
				webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  string(payload.Join.Answer.Sdp),
				}))
		}
	}
}
