package sfu

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/lucsky/cuid"
	sdptransform "github.com/notedit/sdp"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/rtc"
	transport "github.com/pion/ion-sfu/pkg/rtc/transport"
	"github.com/pion/webrtc/v2"

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
	mid := cuid.New()
	shutdownChan := make(chan error)

	go func() {
		var pub *transport.WebRTCTransport
		for {
			in, err := stream.Recv()

			if err == io.EOF || err == context.Canceled {
				log.Infof("publish: close")
				shutdownChan <- nil
				return
			}

			if err != nil {
				log.Errorf("publish error %v", err)
				shutdownChan <- err
				return
			}

			switch payload := in.Payload.(type) {
			case *pb.PublishRequest_Connect:
				log.Infof("publish->connect called: %v", payload.Connect)

				options := payload.Connect.Options
				sdp := payload.Connect.Description.Sdp

				if sdp == "" {
					shutdownChan <- errors.New("publish->connect: sdp invalid")
					return
				}

				offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}

				rtcOptions := transport.RTCOptions{
					Publish:     true,
					Codec:       options.Codec,
					Bandwidth:   options.Bandwidth,
					TransportCC: options.Transportcc,
				}

				videoCodec := strings.ToUpper(rtcOptions.Codec)

				sdpObj, err := sdptransform.Parse(offer.SDP)
				if err != nil {
					log.Errorf("err=%v sdpObj=%v", err, sdpObj)
					shutdownChan <- errors.New("publish->connect: sdp parse failed")
					return
				}

				allowedCodecs := make([]uint8, 0)
				for _, s := range sdpObj.GetStreams() {
					for _, track := range s.GetTracks() {
						pt, _ := getPubPTForTrack(videoCodec, track, sdpObj)

						if len(track.GetSSRCS()) == 0 {
							shutdownChan <- errors.New("publish->connect: ssrc not found")
							return
						}
						allowedCodecs = append(allowedCodecs, pt)
					}
				}

				rtcOptions.Codecs = allowedCodecs
				pub = transport.NewWebRTCTransport(mid, rtcOptions)
				if pub == nil {
					shutdownChan <- errors.New("publish->connect: transport.NewWebRTCTransport failed")
					return
				}

				pub.OnClose(func() {
					shutdownChan <- nil
				})

				router := rtc.GetOrNewRouter(mid)

				answer, err := pub.Answer(offer, rtcOptions)
				if err != nil {
					log.Errorf("publish->connect: error creating answer. err=%v answer=%v", err, answer)
					shutdownChan <- err
					return
				}

				router.AddPub(pub)

				log.Infof("publish->connect: answer => %v", answer)

				err = stream.Send(&pb.PublishReply{
					Mid: mid,
					Payload: &pb.PublishReply_Connect{
						Connect: &pb.Connect{
							Description: &pb.SessionDescription{
								Type: answer.Type.String(),
								Sdp:  answer.SDP,
							},
						},
					},
				})

				if err != nil {
					log.Errorf("publish->connect: error publishing stream: %v", err)
					shutdownChan <- err
					return
				}

				go func() {
					for {
						trickle := <-pub.GetCandidateChan()
						if trickle != nil {
							err = stream.Send(&pb.PublishReply{
								Mid: mid,
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
					shutdownChan <- errors.New("publish->trickle: called before connect")
					return
				}

				if err := pub.AddCandidate(payload.Trickle.Candidate); err != nil {
					shutdownChan <- errors.New("publish->trickle: error adding candidate")
					return
				}
			}
		}
	}()

	err := <-shutdownChan

	log.Infof("Got shutdown for pub %s", mid)
	rtc.DelRouter(mid)

	return err
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
	var router *rtc.Router
	shutdownChan := make(chan error, 1)
	subMid := cuid.New()

	go func() {
		var sub *transport.WebRTCTransport
		for {
			in, err := stream.Recv()
			if err == io.EOF || err == context.Canceled {
				log.Infof("subscribe: close")
				shutdownChan <- nil
				return
			}

			if err != nil {
				log.Errorf("subscribe error %v", err)
				shutdownChan <- nil
				return
			}

			switch payload := in.Payload.(type) {
			case *pb.SubscribeRequest_Connect:
				log.Infof("subscribe->connect called: %v", payload.Connect)
				router = rtc.GetOrNewRouter(in.Mid)

				if router == nil {
					shutdownChan <- errors.New("subscribe->connect: router not found")
					return
				}

				pub := router.GetPub().(*transport.WebRTCTransport)
				sdp := payload.Connect.Description.Sdp

				rtcOptions := transport.RTCOptions{
					Subscribe: true,
				}

				if payload.Connect.Options != nil {
					if payload.Connect.Options.Bandwidth != 0 {
						rtcOptions.Bandwidth = payload.Connect.Options.Bandwidth
					}

					rtcOptions.TransportCC = payload.Connect.Options.Transportcc
				}

				tracks := pub.GetInTracks()
				rtcOptions.Ssrcpt = make(map[uint32]uint8)

				for ssrc, track := range tracks {
					rtcOptions.Ssrcpt[ssrc] = uint8(track.PayloadType())
				}

				sdpObj, err := sdptransform.Parse(sdp)
				if err != nil {
					log.Errorf("err=%v sdpObj=%v", err, sdpObj)
					shutdownChan <- errors.New("subscribe: sdp parse failed")
					return
				}

				ssrcPTMap := make(map[uint32]uint8)
				allowedCodecs := make([]uint8, 0, len(tracks))

				for ssrc, track := range tracks {
					// Find pt for track given track.Payload and sdp
					ssrcPTMap[ssrc] = getSubPTForTrack(track, sdpObj)
					allowedCodecs = append(allowedCodecs, ssrcPTMap[ssrc])
				}

				// Set media engine codecs based on found pts
				log.Infof("Allowed codecs %v", allowedCodecs)
				rtcOptions.Codecs = allowedCodecs

				// New api
				sub = transport.NewWebRTCTransport(subMid, rtcOptions)

				if sub == nil {
					shutdownChan <- errors.New("subscribe->connect: transport.NewWebRTCTransport failed")
					return
				}

				sub.OnClose(func() {
					shutdownChan <- nil
				})

				for ssrc, track := range tracks {
					// Get payload type from request track
					pt := track.PayloadType()
					if newPt, ok := ssrcPTMap[ssrc]; ok {
						// Override with "negotiated" PT
						pt = newPt
					}

					// I2AacsRLsZZriGapnvPKiKBcLi8rTrO1jOpq c84ded42-d2b0-4351-88d2-b7d240c33435
					//                streamID                        trackID
					log.Infof("AddTrack: codec:%s, ssrc:%d, pt:%d, streamID %s, trackID %s", track.Codec().MimeType, ssrc, pt, pub.ID(), track.ID())
					_, err := sub.AddSendTrack(ssrc, pt, pub.ID(), track.ID())
					if err != nil {
						log.Errorf("err=%v", err)
					}
				}

				// Build answer
				offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}
				answer, err := sub.Answer(offer, rtcOptions)
				if err != nil {
					log.Errorf("err=%v answer=%v", err, answer)
					shutdownChan <- errors.New("unsupported media type")
					return
				}

				router.AddSub(subMid, sub)

				log.Infof("subscribe->connect: mid %s, answer = %v", subMid, answer)
				err = stream.Send(&pb.SubscribeReply{
					Mid: subMid,
					Payload: &pb.SubscribeReply_Connect{
						Connect: &pb.Connect{
							Description: &pb.SessionDescription{
								Type: answer.Type.String(),
								Sdp:  answer.SDP,
							},
						},
					},
				})

				if err != nil {
					log.Errorf("subscribe->connect: error subscribing to stream: %v", err)
					return
				}

				go func() {
					for {
						trickle := <-pub.GetCandidateChan()
						if trickle != nil {
							err = stream.Send(&pb.SubscribeReply{
								Mid: subMid,
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
					shutdownChan <- errors.New("subscribe->trickle: called before connect")
					return
				}

				if err := sub.AddCandidate(payload.Trickle.Candidate); err != nil {
					shutdownChan <- errors.New("subscribe->trickle: error adding candidate")
					return
				}
			}
		}
	}()

	err := <-shutdownChan

	log.Infof("Got shutdown for sub %s", subMid)
	if router != nil {
		router.DelSub(subMid)
	}

	return err
}
