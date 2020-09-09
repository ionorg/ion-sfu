// Package cmd contains an entrypoint for running an ion-sfu instance.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	sfu "github.com/pion/ion-sfu/pkg"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/webrtc/v3"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"net"
	"os"
	"sync"

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

// Server struct
type Server struct {
	sfu     *sfu.SFU
	clients sync.Map
}

// NewSFUServer Creates a new sfu server
func NewSFUServer(c sfu.Config) *Server {
	return &Server{
		sfu:     sfu.NewSFU(c),
		clients: sync.Map{},
	}
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

	log.Init(conf.Log.Level)

	log.Infof("--- Starting SFU Node ---")
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Panicf("failed to listen: %v", err)
	}
	log.Infof("SFU Listening at %s", addr)
	s := grpc.NewServer()
	sfuServer := NewSFUServer(conf.Config)
	pb.RegisterSFUService(s, &pb.SFUService{
		Signal: sfuServer.Signal,
		Relay:  sfuServer.Relay,
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
// 2. `Trickle` containing candidate information for Trickle ICE.
//
// If the webrtc connection is closed, the server will close this stream.
//
// The client should send a message containing the session id
// and one of two different payload types:
// 1. `Connect` containing the session offer description. This
// message must *always* be sent first.
// 2. `Trickle` containing candidate information for Trickle ICE.
//
// If the client closes this stream, the webrtc stream will be closed.

// Signal endpoint implementation
func (s *Server) Signal(stream pb.SFU_SignalServer) error {
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

			me := sfu.MediaEngine{}
			err := me.PopulateFromSDP(offer)
			if err != nil {
				log.Errorf("join error: %v", err)
				return status.Errorf(codes.InvalidArgument, "join error %s", err)
			}

			peer, err = s.sfu.NewWebRTCTransport(payload.Join.Sid, me)
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

			answer, err = peer.CreateAnswer()
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
				log.Debugf("on negotiation needed called for pc %s", peer.ID())
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

// Relay endpoint implementation
func (s *Server) Relay(ctx context.Context, request *pb.RelayRequest) (*pb.RelayResponse, error) {
	var sfuClient interface{}
	var session *sfu.Session

	sfuClient, ok := s.clients.Load(request.Sfu)
	if !ok {
		log.Infof("Cannot find client for %s: creating client ...", request.Sfu)
		conn, err := grpc.Dial(request.Sfu, grpc.WithInsecure(), grpc.WithBlock())
		if err != nil {
			return nil, status.Errorf(codes.Internal, "%s", err)
		}
		defer conn.Close()
		sfuClient = pb.NewSFUClient(conn)

		log.Infof("caching client for future use ...")
		s.clients.Store(request.Sfu, sfuClient)
	}

	sfuStream, err := sfuClient.(pb.SFUClient).Signal(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%s", err)
	}

	me := sfu.MediaEngine{}
	me.RegisterDefaultCodecs()

	session = s.sfu.GetSession(request.Sid)
	if session == nil {
		session = s.sfu.NewSession(request.Sid)
	}

	t, err := sfu.NewWebRTCTransport(ctx, session, me, sfu.WebRTCTransportConfig{})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%s", err)
	}

	offer, err := t.CreateOffer()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%s", err)
	}

	if err = t.SetLocalDescription(offer); err != nil {
		return nil, status.Errorf(codes.Internal, "%s", err)
	}

	log.Debugf("Send offer:\n %s", offer.SDP)
	err = sfuStream.Send(&pb.SignalRequest{
		Payload: &pb.SignalRequest_Join{
			Join: &pb.JoinRequest{
				Sid: request.Sid,
				Offer: &pb.SessionDescription{
					Type: offer.Type.String(),
					Sdp:  []byte(offer.SDP),
				},
			},
		},
	})

	if err != nil {
		return nil, status.Errorf(codes.Internal, "%s", err)
	}

	go func() {
		// Handle sfu stream messages
		for {
			res, err := sfuStream.Recv()

			if err != nil {
				if err == io.EOF {
					// WebRTC Transport closed
					log.Infof("WebRTC Transport Closed")
					err = sfuStream.CloseSend()
					if err != nil {
						log.Errorf("error sending close: %s", err)
					}
					return
				}

				errStatus, _ := status.FromError(err)
				if errStatus.Code() == codes.Canceled {
					err = sfuStream.CloseSend()
					if err != nil {
						log.Errorf("error sending close: %s", err)
					}
					return
				}

				log.Errorf("Error receiving signal response: %v", err)
				return
			}

			switch payload := res.Payload.(type) {
			case *pb.SignalReply_Join:
				// Set the remote SessionDescription
				log.Debugf("got answer: %s", string(payload.Join.Answer.Sdp))
				if err = t.SetRemoteDescription(webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  string(payload.Join.Answer.Sdp),
				}); err != nil {
					log.Errorf("join error %s", err)
					return
				}

			case *pb.SignalReply_Negotiate:
				log.Debugf("got negotiate %s", payload.Negotiate.Type)
				if payload.Negotiate.Type == webrtc.SDPTypeOffer.String() {
					log.Debugf("got offer: %s", string(payload.Negotiate.Sdp))
					offer := webrtc.SessionDescription{
						Type: webrtc.SDPTypeOffer,
						SDP:  string(payload.Negotiate.Sdp),
					}

					// Peer exists, renegotiating existing peer
					err = t.SetRemoteDescription(offer)
					if err != nil {
						log.Errorf("negotiate error %s", err)
						continue
					}

					var answer webrtc.SessionDescription
					answer, err = t.CreateAnswer()
					if err != nil {
						log.Errorf("negotiate error %s", err)
						continue
					}

					err = t.SetLocalDescription(answer)
					if err != nil {
						log.Errorf("negotiate error %s", err)
						continue
					}

					err = sfuStream.Send(&pb.SignalRequest{
						Payload: &pb.SignalRequest_Negotiate{
							Negotiate: &pb.SessionDescription{
								Type: answer.Type.String(),
								Sdp:  []byte(answer.SDP),
							},
						},
					})

					if err != nil {
						log.Errorf("negotiate error %s", err)
						continue
					}
				} else if payload.Negotiate.Type == webrtc.SDPTypeAnswer.String() {
					log.Debugf("got answer: %s", string(payload.Negotiate.Sdp))
					err = t.SetRemoteDescription(webrtc.SessionDescription{
						Type: webrtc.SDPTypeAnswer,
						SDP:  string(payload.Negotiate.Sdp),
					})

					if err != nil {
						log.Errorf("negotiate error %s", err)
						continue
					}
				}
			case *pb.SignalReply_Trickle:
				var candidate webrtc.ICECandidateInit
				_ = json.Unmarshal([]byte(payload.Trickle.Init), &candidate)
				err := t.AddICECandidate(candidate)
				if err != nil {
					log.Errorf("error adding ice candidate: %e", err)
				}
			}
		}
	}()

	return &pb.RelayResponse{}, nil
}
