package sfu

import (
	"testing"

	pb "github.com/pion/ion-sfu/pkg/proto"
	"google.golang.org/grpc"
)

func TestPublishReturnsErrorWithInvalidSDP(t *testing.T) {
	sfu := &server{}
	s := grpc.NewServer()
	pb.RegisterSFUServer(s, sfu)

	request := pb.PublishRequest_Connect{
		Connect: &pb.Connect{
			Options: &pb.Options{},
			Description: &pb.SessionDescription{
				Type: "offer",
				Sdp:  "invalid",
			},
		},
	}

	_, _, err := sfu.publish(&request)

	if err != errSdpParseFailed {
		t.Fatal("Should return error on invalid sdp")
	}
}
