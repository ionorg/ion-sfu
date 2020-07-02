package sfu

import (
	"errors"
	"io"

	"github.com/pion/ion-sfu/pkg/log"
	transport "github.com/pion/ion-sfu/pkg/rtc/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/pion/ion-sfu/pkg/proto"
)

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
func (s *server) Publish(stream pb.SFU_PublishServer) error {
	var pub *transport.WebRTCTransport
	for {
		in, err := stream.Recv()

		if err != nil {
			if pub != nil {
				pub.Close()
			}

			if err == io.EOF {
				return nil
			}

			errStatus, _ := status.FromError(err)
			if errStatus.Code() == codes.Canceled {
				return nil
			}

			log.Errorf("publish error %v %v", errStatus.Message(), errStatus.Code())
			return err
		}

		switch payload := in.Payload.(type) {
		case *pb.PublishRequest_Connect:
			var reply *pb.PublishReply_Connect
			log.Infof("publish->connect called: %v", payload.Connect)

			pub, reply, err = s.publish(payload)

			if err != nil {
				log.Errorf("publish->connect: error publishing stream: %v", err)
				pub.Close()
				return err
			}

			err = stream.Send(&pb.PublishReply{
				Mid:     pub.ID(),
				Payload: reply,
			})

			if err != nil {
				log.Errorf("publish->connect: error publishing stream: %v", err)
				pub.Close()
				return err
			}

			// TODO: Close
			go func() {
				for {
					trickle := <-pub.GetCandidateChan()
					if trickle != nil {
						err = stream.Send(&pb.PublishReply{
							Mid: pub.ID(),
							Payload: &pb.PublishReply_Trickle{
								Trickle: &pb.Trickle{
									Candidate: trickle.String(),
								},
							},
						})
					} else {
						return
					}
				}
			}()

		case *pb.PublishRequest_Trickle:
			if pub == nil {
				return errors.New("publish->trickle: called before connect")
			}

			if err := pub.AddCandidate(payload.Trickle.Candidate); err != nil {
				return errors.New("publish->trickle: error adding candidate")
			}
		}
	}
}

// Subscribe to a stream from the sfu. Subscribe creates a bidirectional
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
func (s *server) Subscribe(stream pb.SFU_SubscribeServer) error {
	var sub *transport.WebRTCTransport
	for {
		in, err := stream.Recv()

		if err != nil {
			if sub != nil {
				sub.Close()
			}

			if err == io.EOF {
				return nil
			}

			errStatus, _ := status.FromError(err)
			if errStatus.Code() == codes.Canceled {
				return nil
			}

			log.Errorf("subscribe error %v", err)
			return err
		}

		switch payload := in.Payload.(type) {
		case *pb.SubscribeRequest_Connect:
			var reply *pb.SubscribeReply_Connect
			sub, reply, err = s.subscribe(in.Mid, payload)

			if err != nil {
				log.Errorf("subscribe->connect: error subscribing stream: %v", err)
				return err
			}

			err = stream.Send(&pb.SubscribeReply{
				Mid:     sub.ID(),
				Payload: reply,
			})

			if err != nil {
				log.Errorf("subscribe->connect: error subscribing to stream: %v", err)
				sub.Close()
				return nil
			}

			// TODO: Close
			go func() {
				for {
					trickle := <-sub.GetCandidateChan()
					if trickle != nil {
						err = stream.Send(&pb.SubscribeReply{
							Mid: sub.ID(),
							Payload: &pb.SubscribeReply_Trickle{
								Trickle: &pb.Trickle{
									Candidate: trickle.String(),
								},
							},
						})
					} else {
						return
					}
				}
			}()

		case *pb.SubscribeRequest_Trickle:
			if sub == nil {
				return errors.New("subscribe->trickle: called before connect")
			}

			if err := sub.AddCandidate(payload.Trickle.Candidate); err != nil {
				return errors.New("subscribe->trickle: error adding candidate")
			}
		}
	}
}
