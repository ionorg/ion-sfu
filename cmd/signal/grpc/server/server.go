package server

import (
	"encoding/json"
	"fmt"
	"io"

	log "github.com/pion/ion-log"
	sfu "github.com/pion/ion-sfu/pkg"
	"github.com/pion/webrtc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/pion/ion-sfu/cmd/signal/grpc/proto"
)

func NewServer(sfu *sfu.SFU) *grpc.Server {
	s := grpc.NewServer()
	pb.RegisterSFUServer(s, &SFUServer{SFU: sfu})
	return s
}

type SFUServer struct {
	pb.UnimplementedSFUServer
	SFU *sfu.SFU
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
func (s *SFUServer) Signal(stream pb.SFU_SignalServer) error {
	peer := sfu.NewPeer(s.SFU)
	for {
		in, err := stream.Recv()

		if err != nil {
			peer.Close()

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
			log.Debugf("signal->join called:\n%v", string(payload.Join.Description))

			var offer webrtc.SessionDescription
			err := json.Unmarshal(payload.Join.Description, &offer)
			if err != nil {
				return status.Errorf(codes.Internal, fmt.Sprintf("sdp unmarshal error: %v", err))
			}

			answer, err := peer.Join(payload.Join.Sid, offer)
			if err != nil {
				switch err {
				case sfu.ErrTransportExists:
					log.Errorf("peer already exists")
					return status.Errorf(codes.FailedPrecondition, err.Error())
				default:
					return status.Errorf(codes.Internal, fmt.Sprintf("join error: %v", err))
				}
			}

			// Notify user of new ice candidate
			peer.OnIceCandidate = func(candidate *webrtc.ICECandidateInit) {
				bytes, err := json.Marshal(candidate)
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
					log.Errorf("OnIceCandidate error %v ", err)
				}
			}

			// Notify user of new offer
			peer.OnOffer = func(o *webrtc.SessionDescription) {
				marshalled, err := json.Marshal(o)
				if err != nil {
					log.Errorf("sdp marshal error: %v", err)
					return
				}

				err = stream.Send(&pb.SignalReply{
					Payload: &pb.SignalReply_Description{
						Description: marshalled,
					},
				})

				if err != nil {
					log.Errorf("negotiation error %s", err)
				}
			}

			marshalled, err := json.Marshal(answer)
			if err != nil {
				return status.Errorf(codes.Internal, fmt.Sprintf("sdp marshal error: %v", err))
			}

			// send answer
			err = stream.Send(&pb.SignalReply{
				Id: in.Id,
				Payload: &pb.SignalReply_Join{
					Join: &pb.JoinReply{
						Description: marshalled,
					},
				},
			})

			if err != nil {
				log.Errorf("error sending join response %s", err)
				return status.Errorf(codes.Internal, "join error %s", err)
			}

		case *pb.SignalRequest_Description:
			var sdp webrtc.SessionDescription
			err := json.Unmarshal(payload.Description, &sdp)
			if err != nil {
				return status.Errorf(codes.Internal, fmt.Sprintf("sdp unmarshal error: %v", err))
			}

			if sdp.Type == webrtc.SDPTypeOffer {
				answer, err := peer.Answer(sdp)
				if err != nil {
					switch err {
					case sfu.ErrNoTransportEstablished:
						return status.Errorf(codes.FailedPrecondition, err.Error())
					default:
						return status.Errorf(codes.Internal, fmt.Sprintf("negotiate error: %v", err))
					}
				}

				marshalled, err := json.Marshal(answer)
				if err != nil {
					return status.Errorf(codes.Internal, fmt.Sprintf("sdp marshal error: %v", err))
				}

				err = stream.Send(&pb.SignalReply{
					Id: in.Id,
					Payload: &pb.SignalReply_Description{
						Description: marshalled,
					},
				})

				if err != nil {
					return status.Errorf(codes.Internal, fmt.Sprintf("negotiate error: %v", err))
				}

			} else if sdp.Type == webrtc.SDPTypeAnswer {
				err := peer.SetRemoteDescription(sdp)
				if err != nil {
					switch err {
					case sfu.ErrNoTransportEstablished:
						return status.Errorf(codes.FailedPrecondition, err.Error())
					default:
						return status.Errorf(codes.Internal, fmt.Sprintf("negotiate error: %v", err))
					}
				}
			}

		case *pb.SignalRequest_Trickle:
			var candidate webrtc.ICECandidateInit
			err := json.Unmarshal([]byte(payload.Trickle.Init), &candidate)
			if err != nil {
				log.Errorf("error parsing ice candidate: %v", err)
			}

			err = peer.Trickle(candidate)
			if err != nil {
				switch err {
				case sfu.ErrNoTransportEstablished:
					log.Errorf("peer hasn't joined")
					return status.Errorf(codes.FailedPrecondition, err.Error())
				default:
					return status.Errorf(codes.Internal, fmt.Sprintf("negotiate error: %v", err))
				}
			}

		}
	}
}
