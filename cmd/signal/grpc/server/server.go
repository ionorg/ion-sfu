package server

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	log "github.com/pion/ion-log"
	pb "github.com/pion/ion-sfu/cmd/signal/grpc/proto"
	"github.com/pion/ion-sfu/pkg/sfu"
	"github.com/pion/webrtc/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type SFUServer struct {
	pb.UnimplementedSFUServer
	SFU *sfu.SFU
}

func NewServer(sfu *sfu.SFU) *SFUServer {
	return &SFUServer{SFU: sfu}
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
	var streamLock sync.Mutex
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
				streamLock.Lock()
				err = stream.Send(&pb.SignalReply{
					Payload: &pb.SignalReply_Error{
						Error: fmt.Errorf("join sdp unmarshal error: %w", err).Error(),
					},
				})
				streamLock.Unlock()
				if err != nil {
					log.Errorf("grpc send error %v ", err)
					return status.Errorf(codes.Internal, err.Error())
				}
			}

			// Notify user of new ice candidate
			peer.OnIceCandidate = func(candidate *webrtc.ICECandidateInit, target int) {
				bytes, err := json.Marshal(candidate)
				if err != nil {
					log.Errorf("OnIceCandidate error %s", err)
				}
				streamLock.Lock()
				err = stream.Send(&pb.SignalReply{
					Payload: &pb.SignalReply_Trickle{
						Trickle: &pb.Trickle{
							Init:   string(bytes),
							Target: pb.Trickle_Target(target),
						},
					},
				})
				streamLock.Unlock()
				if err != nil {
					log.Errorf("OnIceCandidate send error %v ", err)
				}
			}

			// Notify user of new offer
			peer.OnOffer = func(o *webrtc.SessionDescription) {
				marshalled, err := json.Marshal(o)
				if err != nil {
					streamLock.Lock()
					err = stream.Send(&pb.SignalReply{
						Payload: &pb.SignalReply_Error{
							Error: fmt.Errorf("offer sdp marshal error: %w", err).Error(),
						},
					})
					streamLock.Unlock()
					if err != nil {
						log.Errorf("grpc send error %v ", err)
					}
					return
				}

				streamLock.Lock()
				err = stream.Send(&pb.SignalReply{
					Payload: &pb.SignalReply_Description{
						Description: marshalled,
					},
				})
				streamLock.Unlock()

				if err != nil {
					log.Errorf("negotiation error %s", err)
				}
			}

			peer.OnICEConnectionStateChange = func(c webrtc.ICEConnectionState) {
				streamLock.Lock()
				err = stream.Send(&pb.SignalReply{
					Payload: &pb.SignalReply_IceConnectionState{
						IceConnectionState: c.String(),
					},
				})
				streamLock.Unlock()

				if err != nil {
					log.Errorf("oniceconnectionstatechange error %s", err)
				}
			}

			answer, err := peer.Join(payload.Join.Sid, offer)
			if err != nil {
				switch err {
				case sfu.ErrTransportExists:
					fallthrough
				case sfu.ErrOfferIgnored:
					streamLock.Lock()
					err = stream.Send(&pb.SignalReply{
						Payload: &pb.SignalReply_Error{
							Error: fmt.Errorf("join error: %w", err).Error(),
						},
					})
					streamLock.Unlock()
					if err != nil {
						log.Errorf("grpc send error %v ", err)
						return status.Errorf(codes.Internal, err.Error())
					}
				default:
					return status.Errorf(codes.Unknown, err.Error())
				}
			}

			marshalled, err := json.Marshal(answer)
			if err != nil {
				return status.Errorf(codes.Internal, fmt.Sprintf("sdp marshal error: %v", err))
			}

			// send answer
			streamLock.Lock()
			err = stream.Send(&pb.SignalReply{
				Id: in.Id,
				Payload: &pb.SignalReply_Join{
					Join: &pb.JoinReply{
						Description: marshalled,
					},
				},
			})
			streamLock.Unlock()

			if err != nil {
				log.Errorf("error sending join response %s", err)
				return status.Errorf(codes.Internal, "join error %s", err)
			}

		case *pb.SignalRequest_Description:
			var sdp webrtc.SessionDescription
			err := json.Unmarshal(payload.Description, &sdp)
			if err != nil {
				streamLock.Lock()
				err = stream.Send(&pb.SignalReply{
					Payload: &pb.SignalReply_Error{
						Error: fmt.Errorf("negotiate sdp unmarshal error: %w", err).Error(),
					},
				})
				streamLock.Unlock()
				if err != nil {
					log.Errorf("grpc send error %v ", err)
					return status.Errorf(codes.Internal, err.Error())
				}
			}

			if sdp.Type == webrtc.SDPTypeOffer {
				answer, err := peer.Answer(sdp)
				if err != nil {
					switch err {
					case sfu.ErrNoTransportEstablished:
						fallthrough
					case sfu.ErrOfferIgnored:
						streamLock.Lock()
						err = stream.Send(&pb.SignalReply{
							Payload: &pb.SignalReply_Error{
								Error: fmt.Errorf("negotiate answer error: %w", err).Error(),
							},
						})
						streamLock.Unlock()
						if err != nil {
							log.Errorf("grpc send error %v ", err)
							return status.Errorf(codes.Internal, err.Error())
						}
						continue
					default:
						return status.Errorf(codes.Unknown, fmt.Sprintf("negotiate error: %v", err))
					}
				}

				marshalled, err := json.Marshal(answer)
				if err != nil {
					streamLock.Lock()
					err = stream.Send(&pb.SignalReply{
						Payload: &pb.SignalReply_Error{
							Error: fmt.Errorf("sdp marshal error: %w", err).Error(),
						},
					})
					streamLock.Unlock()
					if err != nil {
						log.Errorf("grpc send error %v ", err)
						return status.Errorf(codes.Internal, err.Error())
					}
				}

				streamLock.Lock()
				err = stream.Send(&pb.SignalReply{
					Id: in.Id,
					Payload: &pb.SignalReply_Description{
						Description: marshalled,
					},
				})
				streamLock.Unlock()

				if err != nil {
					return status.Errorf(codes.Internal, fmt.Sprintf("negotiate error: %v", err))
				}

			} else if sdp.Type == webrtc.SDPTypeAnswer {
				err := peer.SetRemoteDescription(sdp)
				if err != nil {
					switch err {
					case sfu.ErrNoTransportEstablished:
						streamLock.Lock()
						err = stream.Send(&pb.SignalReply{
							Payload: &pb.SignalReply_Error{
								Error: fmt.Errorf("set remote description error: %w", err).Error(),
							},
						})
						streamLock.Unlock()
						if err != nil {
							log.Errorf("grpc send error %v ", err)
							return status.Errorf(codes.Internal, err.Error())
						}
					default:
						return status.Errorf(codes.Unknown, err.Error())
					}
				}
			}

		case *pb.SignalRequest_Trickle:
			var candidate webrtc.ICECandidateInit
			err := json.Unmarshal([]byte(payload.Trickle.Init), &candidate)
			if err != nil {
				log.Errorf("error parsing ice candidate: %v", err)
				streamLock.Lock()
				err = stream.Send(&pb.SignalReply{
					Payload: &pb.SignalReply_Error{
						Error: fmt.Errorf("unmarshal ice candidate error:  %w", err).Error(),
					},
				})
				streamLock.Unlock()
				if err != nil {
					log.Errorf("grpc send error %v ", err)
					return status.Errorf(codes.Internal, err.Error())
				}
				continue
			}

			err = peer.Trickle(candidate, int(payload.Trickle.Target))
			if err != nil {
				switch err {
				case sfu.ErrNoTransportEstablished:
					log.Errorf("peer hasn't joined")
					streamLock.Lock()
					err = stream.Send(&pb.SignalReply{
						Payload: &pb.SignalReply_Error{
							Error: fmt.Errorf("trickle error:  %w", err).Error(),
						},
					})
					streamLock.Unlock()
					if err != nil {
						log.Errorf("grpc send error %v ", err)
						return status.Errorf(codes.Internal, err.Error())
					}
				default:
					return status.Errorf(codes.Unknown, fmt.Sprintf("negotiate error: %v", err))
				}
			}

		}
	}
}
