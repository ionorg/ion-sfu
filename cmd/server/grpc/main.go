// Package cmd contains an entrypoint for running an ion-sfu instance.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"

	sfu "github.com/pion/ion-sfu/pkg"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/webrtc/v3"
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
	conf      = Config{}
	file      string
	addr      string
	errNoPeer = errors.New("no peer exists")
)

type server struct {
	pb.UnimplementedSFUServer
	sfu *sfu.SFU
}

const (
	portRangeLimit = 100
)

func showHelp() {
	fmt.Printf("Usage:%s {params}\n", os.Args[0])
	fmt.Println("      -c {config file}")
	fmt.Println("      -a {listen addr}")
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

	if len(conf.WebRTC.ICEPortRange) != 0 && conf.WebRTC.ICEPortRange[1]-conf.WebRTC.ICEPortRange[0] < portRangeLimit {
		fmt.Printf("config file %s loaded failed. range port must be [min, max] and max - min >= %d\n", file, portRangeLimit)
		return false
	}

	fmt.Printf("config %s load ok!\n", file)
	return true
}

func parse() bool {
	flag.StringVar(&file, "c", "config.toml", "config file")
	flag.StringVar(&addr, "a", ":50051", "address to use")
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

	log.Infof("--- Starting SFU Node ---")
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Panicf("failed to listen: %v", err)
	}
	log.Infof("SFU Listening at %s", addr)
	s := grpc.NewServer()
	pb.RegisterSFUServer(s, &server{
		sfu: sfu.NewSFU(conf.Config),
	})
	if err := s.Serve(lis); err != nil {
		log.Panicf("failed to serve: %v", err)
	}
	select {}
}

// Publish a stream to the sfu. Publish creates a bidirectional
// streaming rpc connection between the client and sfu.
//
// The sfu will respond with a message containing the stream pid
// and one of two different payload types:
// 1. `Connect` containing the session answer description. This
// message is *always* returned first.
// 2. `Trickle` containg candidate information for Trickle ICE.
//
// If the webrtc connection is closed, the server will close this stream.
//
// The client should send a message containg the session id
// and one of two different payload types:
// 1. `Connect` containing the session offer description. This
// message must *always* be sent first.
// 2. `Trickle` containing candidate information for Trickle ICE.
//
// If the client closes this stream, the webrtc stream will be closed.
func (s *server) Signal(stream pb.SFU_SignalServer) error {
	var pid string
	var peer *sfu.WebRTCTransport
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
		case *pb.SignalRequest_Join:
			var answer webrtc.SessionDescription
			log.Infof("signal->join called:\n%v", string(payload.Join.Offer.Sdp))

			if peer != nil {
				// already joined
				log.Errorf("peer already exists")
				return status.Errorf(codes.FailedPrecondition, "peer already exists")
			}

			offer := webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  string(payload.Join.Offer.Sdp),
			}

			peer, err = s.sfu.NewWebRTCTransport(payload.Join.Sid, offer)
			if err != nil {
				log.Errorf("join error: %v", err)
				return status.Errorf(codes.InvalidArgument, "join error %s", err)
			}

			log.Infof("peer %s join session %s", peer.ID(), payload.Join.Sid)

			err = peer.SetRemoteDescription(offer)
			if err != nil {
				log.Errorf("join error: %v", err)
				return status.Errorf(codes.Internal, "join error %s", err)
			}

			answer, err := peer.CreateAnswer()
			if err != nil {
				log.Errorf("join error: %v", err)
				return status.Errorf(codes.Internal, "join error %s", err)
			}

			err = peer.SetLocalDescription(answer)
			if err != nil {
				log.Errorf("join error: %v", err)
				return status.Errorf(codes.Internal, "join error %s", err)
			}

			// Notify user of trickle candidates
			peer.OnICECandidate(func(c *webrtc.ICECandidate) {
				if c == nil {
					// Gathering done
					return
				}
				bytes, err := json.Marshal(c.ToJSON())
				if err != nil {
					log.Errorf("OnIceCandidate error %s", err)
				}

				err = stream.Send(&pb.SignalReply{
					Payload: &pb.SignalReply_Trickle{
						Trickle: &pb.Trickle{
							Init: string(bytes),
						},
					},
				})
				if err != nil {
					log.Errorf("OnIceCandidate error %s", err)
				}
			})

			peer.OnNegotiationNeeded(func() {
				log.Debugf("on negotiation needed called")
				offer, err := peer.CreateOffer()
				if err != nil {
					log.Errorf("CreateOffer error: %v", err)
					return
				}

				err = peer.SetLocalDescription(offer)
				if err != nil {
					log.Errorf("SetLocalDescription error: %v", err)
					return
				}

				err = stream.Send(&pb.SignalReply{
					Payload: &pb.SignalReply_Negotiate{
						Negotiate: &pb.SessionDescription{
							Type: offer.Type.String(),
							Sdp:  []byte(offer.SDP),
						},
					},
				})

				if err != nil {
					log.Errorf("negotiation error %s", err)
				}
			})

			err = stream.Send(&pb.SignalReply{
				Payload: &pb.SignalReply_Join{
					Join: &pb.JoinReply{
						Pid: pid,
						Answer: &pb.SessionDescription{
							Type: answer.Type.String(),
							Sdp:  []byte(answer.SDP),
						},
					},
				},
			})

			if err != nil {
				log.Errorf("error sending join response %s", err)
				return status.Errorf(codes.Internal, "join error %s", err)
			}

		case *pb.SignalRequest_Negotiate:
			if peer == nil {
				return status.Errorf(codes.FailedPrecondition, "%s", errNoPeer)
			}

			if payload.Negotiate.Type == webrtc.SDPTypeOffer.String() {
				offer := webrtc.SessionDescription{
					Type: webrtc.SDPTypeOffer,
					SDP:  string(payload.Negotiate.Sdp),
				}

				// Peer exists, renegotiating existing peer
				err = peer.SetRemoteDescription(offer)
				if err != nil {
					return status.Errorf(codes.Internal, "%s", err)
				}

				answer, err := peer.CreateAnswer()
				if err != nil {
					return status.Errorf(codes.Internal, "%s", err)
				}

				err = peer.SetLocalDescription(answer)
				if err != nil {
					return status.Errorf(codes.Internal, "%s", err)
				}

				err = stream.Send(&pb.SignalReply{
					Payload: &pb.SignalReply_Negotiate{
						Negotiate: &pb.SessionDescription{
							Type: answer.Type.String(),
							Sdp:  []byte(answer.SDP),
						},
					},
				})
				if err != nil {
					return status.Errorf(codes.Internal, "%s", err)
				}
			} else if payload.Negotiate.Type == webrtc.SDPTypeAnswer.String() {
				err = peer.SetRemoteDescription(webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  string(payload.Negotiate.Sdp),
				})

				if err != nil {
					return status.Errorf(codes.Internal, "%s", err)
				}
			}

		case *pb.SignalRequest_Trickle:
			if peer == nil {
				return status.Errorf(codes.FailedPrecondition, "%s", errNoPeer)
			}

			var candidate webrtc.ICECandidateInit
			err := json.Unmarshal([]byte(payload.Trickle.Init), &candidate)
			if err != nil {
				log.Errorf("error parsing ice candidate: %v", err)
			}

			if err := peer.AddICECandidate(candidate); err != nil {
				return status.Errorf(codes.Internal, "error adding ice candidate")
			}
		}
	}
}
