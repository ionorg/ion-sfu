package sfu

import (
	"net"

	"github.com/pion/ion-sfu/pkg/log"
	"google.golang.org/grpc"

	pb "github.com/pion/ion-sfu/pkg/proto/sfu"
)

type server struct {
	pb.UnimplementedSFUServer
}

// InitLogLevel for sfu
func InitLogLevel(level string) {
	log.Init(level)
}

// Init func
func Init(port string) {
	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Panicf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterSFUServer(s, &server{})
	if err := s.Serve(lis); err != nil {
		log.Panicf("failed to serve: %v", err)
	}
}
