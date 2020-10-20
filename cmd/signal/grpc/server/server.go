package server

import (
	"encoding/json"
	"fmt"
	"io"

	sfu "github.com/pion/ion-sfu/pkg"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/webrtc/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/pion/ion-sfu/cmd/signal/grpc/proto"
)

type GRPCSignal struct {
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
func (s *GRPCSignal) Signal(stream pb.SFU_SignalServer) error {
	var pid string
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
			log.Infof("signal->join called:\n%v", string(payload.Join.Offer.Sdp))

			offer := webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  string(payload.Join.Offer.Sdp),
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
				err := stream.Send(&pb.SignalReply{
					Payload: &pb.SignalReply_Negotiate{
						Negotiate: &pb.SessionDescription{
							Type: o.Type.String(),
							Sdp:  []byte(o.SDP),
						},
					},
				})

				if err != nil {
					log.Errorf("negotiation error %s", err)
				}
			}

			// send answer
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
			if payload.Negotiate.Type == webrtc.SDPTypeOffer.String() {
				offer := webrtc.SessionDescription{
					Type: webrtc.SDPTypeOffer,
					SDP:  string(payload.Negotiate.Sdp),
				}

				answer, err := peer.Answer(offer)
				if err != nil {
					switch err {
					case sfu.ErrNoTransportEstablished:
						log.Errorf("peer hasn't joined")
						return status.Errorf(codes.FailedPrecondition, err.Error())
					default:
						return status.Errorf(codes.Internal, fmt.Sprintf("negotiate error: %v", err))
					}
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
					return status.Errorf(codes.Internal, fmt.Sprintf("negotiate error: %v", err))
				}

			} else if payload.Negotiate.Type == webrtc.SDPTypeAnswer.String() {
				answer := webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  string(payload.Negotiate.Sdp),
				}

				err := peer.SetRemoteDescription(answer)
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
