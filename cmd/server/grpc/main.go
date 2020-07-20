// Package cmd contains an entrypoint for running an ion-sfu instance.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"

	sfu "github.com/pion/ion-sfu/pkg"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/webrtc/v2"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/pion/ion-sfu/cmd/server/grpc/proto"
)

type grpcConfig struct {
	Port string `mapstructure:"port"`
}

// Config defines parameters for configuring the sfu instance
type Config struct {
	sfu.Config `mapstructure:",squash"`
	GRPC       grpcConfig `mapstructure:"grpc"`
}

var (
	conf = Config{}
	node *sfu.SFU
	file string
)

type server struct {
	pb.UnimplementedSFUServer
}

const (
	portRangeLimit = 100
)

func showHelp() {
	fmt.Printf("Usage:%s {params}\n", os.Args[0])
	fmt.Println("      -c {config file}")
	fmt.Println("      -h (show help info)")
}

func load() bool {
	_, err := os.Stat(file)
	if err != nil {
		return false
	}

	viper.SetConfigFile(file)
	viper.SetConfigType("toml")

	err = viper.ReadInConfig()
	if err != nil {
		fmt.Printf("config file %s read failed. %v\n", file, err)
		return false
	}
	err = viper.GetViper().Unmarshal(&conf)
	if err != nil {
		fmt.Printf("sfu config file %s loaded failed. %v\n", file, err)
		return false
	}

	if len(conf.WebRTC.ICEPortRange) > 2 {
		fmt.Printf("config file %s loaded failed. range port must be [min,max]\n", file)
		return false
	}

	if len(conf.WebRTC.ICEPortRange) != 0 && conf.WebRTC.ICEPortRange[1]-conf.WebRTC.ICEPortRange[0] <= portRangeLimit {
		fmt.Printf("config file %s loaded failed. range port must be [min, max] and max - min >= %d\n", file, portRangeLimit)
		return false
	}

	fmt.Printf("config %s load ok!\n", file)
	return true
}

func parse() bool {
	flag.StringVar(&file, "c", "config.toml", "config file")
	help := flag.Bool("h", false, "help info")
	flag.Parse()
	if !load() {
		return false
	}

	if *help {
		showHelp()
		return false
	}
	return true
}

func main() {
	if !parse() {
		showHelp()
		os.Exit(-1)
	}

	node = sfu.NewSFU(conf.Config)
	log.Infof("--- Starting SFU Node ---")
	lis, err := net.Listen("tcp", conf.GRPC.Port)
	if err != nil {
		log.Panicf("failed to listen: %v", err)
	}
	log.Infof("SFU Listening at %s", conf.GRPC.Port)
	s := grpc.NewServer()
	pb.RegisterSFUServer(s, &server{})
	if err := s.Serve(lis); err != nil {
		log.Panicf("failed to serve: %v", err)
	}
	select {}
}

// Publish a stream to the sfu. Publish creates a bidirectional
// streaming rpc connection between the client and sfu.
//
// The sfu will respond with a message containing the stream mid
// and one of two different payload types:
// 1. `Connect` containing the session answer description. This
// message is *always* returned first.
// 2. `Trickle` containg candidate information for Trickle ICE.
//
// If the webrtc connection is closed, the server will close this stream.
//
// The client should send a message containg the room id
// and one of two different payload types:
// 1. `Connect` containing the session offer description. This
// message must *always* be sent first.
// 2. `Trickle` containing candidate information for Trickle ICE.
//
// If the client closes this stream, the webrtc stream will be closed.
func (s *server) Signal(stream pb.SFU_SignalServer) error {
	var mid string
	var peer *sfu.Peer
	for {
		in, err := stream.Recv()

		if err != nil {
			if peer != nil {
				peer.Close()
			}

			if err == io.EOF {
				return nil
			}

			errStatus, _ := status.FromError(err)
			if errStatus.Code() == codes.Canceled {
				return nil
			}

			log.Errorf("signal error %v %v", errStatus.Message(), errStatus.Code())
			return err
		}

		switch payload := in.Payload.(type) {
		case *pb.SignalRequest_Connect:
			var answer *webrtc.SessionDescription
			log.Infof("signal->connect called: %v", payload.Connect)

			peer, answer, err = node.Connect(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  string(payload.Connect.Sdp),
			})
			mid = peer.ID()
			peer.OnTrack(func(track *webrtc.Track) {
				log.Infof("got track")
			})

			if err != nil {
				log.Errorf("signal->connect: error publishing stream: %v", err)
				return err
			}

			err = stream.Send(&pb.SignalReply{
				Payload: &pb.SignalReply_Connect{
					Connect: &pb.ConnectReply{
						Mid: mid,
						Answer: &pb.SessionDescription{
							Type: answer.Type.String(),
							Sdp:  []byte(answer.SDP),
						},
					},
				},
			})

			if err != nil {
				log.Errorf("signal->connect: error publishing stream: %v", err)
				peer.Close()
				return err
			}

			peer.OnICECandidate(func(c *webrtc.ICECandidate) {
				err = stream.Send(&pb.SignalReply{
					Payload: &pb.SignalReply_Trickle{
						Trickle: &pb.Trickle{
							Candidate: c.String(),
						},
					},
				})
			})

		case *pb.SignalRequest_Trickle:
			if peer == nil {
				return errors.New("signal->trickle: called before connect")
			}

			if err := peer.AddICECandidate(webrtc.ICECandidateInit{
				Candidate: payload.Trickle.Candidate,
			}); err != nil {
				return errors.New("signal->trickle: error adding candidate")
			}
		}
	}
}
