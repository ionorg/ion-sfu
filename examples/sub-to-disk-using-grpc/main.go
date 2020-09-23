// Package sub-to-disk-using-grpc demonstrates how to subscribe a stream from ion-sfu,
// and save VP8/Opus to disk.
package main

import (
	"context"
	"encoding/json"
	"io"

	"os"
	"time"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media/ivfwriter"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sfu "github.com/pion/ion-sfu/cmd/server/grpc/proto"
)

const (
	address       = "localhost:50051"
	audioFileName = "output.ogg"
	videoFileName = "output.ivf"
)

func main() {
	log.Init("debug", []string{"proc.go", "asm_amd64.s", "jsonrpc2.go"})

	// Set up a connection to the sfu server.
	conn, err := grpc.Dial(address, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Panicf("did not connect: %v", err)
	}
	defer conn.Close()
	c := sfu.NewSFUClient(conn)

	// Create a MediaEngine object to configure the supported codec
	m := webrtc.MediaEngine{}

	// Setup the codecs you want to use.
	// We'll use a VP8 codec but you can also define your own
	m.RegisterCodec(webrtc.NewRTPOpusCodec(webrtc.DefaultPayloadTypeOpus, 48000))
	m.RegisterCodec(webrtc.NewRTPVP8Codec(webrtc.DefaultPayloadTypeVP8, 90000))

	// Create the API object with the MediaEngine
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m))

	// Prepare the configuration
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	// Create a new RTCPeerConnection
	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}

	// Allow us to receive 1 audio track, and 1 video track
	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio); err != nil {
		panic(err)
	} else if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo); err != nil {
		panic(err)
	}

	oggFile, err := oggwriter.New(audioFileName, 48000, 2)
	if err != nil {
		panic(err)
	}
	defer oggFile.Close()

	ivfFile, err := ivfwriter.New(videoFileName)
	if err != nil {
		panic(err)
	}
	defer ivfFile.Close()

	// Set a handler for when a new remote track starts, this handler saves buffers to disk as
	// an ivf file, since we could have multiple video tracks we provide a counter.
	// In your application this is where you would handle/process video
	peerConnection.OnTrack(func(track *webrtc.Track, receiver *webrtc.RTPReceiver) {
		log.Debugf("peerConnection.OnTrack")

		// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
		go func() {
			ticker := time.NewTicker(time.Second * 3)
			for range ticker.C {
				errSend := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: track.SSRC()}})
				if errSend != nil {
					log.Errorf("WriteRTCP error: %s", errSend)
				}
			}
		}()

		codec := track.Codec()
		if codec.Name == webrtc.Opus {
			log.Debugf("Got Opus track, saving to disk as output.opus (48 kHz, 2 channels)")
			go func() {
				for {
					pkt, err := track.ReadRTP()
					if err != nil {
						if err == io.EOF {
							log.Debugf("All audio frames reveived")
							peerConnection.Close()
							os.Exit(0)
							return
						}
						log.Panicf("Error track.ReadRTP: ", err)
					}

					//fmt.Printf("%s audio, sequence=%d, timestamp=%d, payload=%d\n", time.Now().Format("15:04:05.000000"), pkt.SequenceNumber, pkt.Timestamp, len(pkt.Payload))
					if err := oggFile.WriteRTP(pkt); err != nil {
						log.Panicf("Error write ogg: ", err)
					}
				}
			}()
		} else if codec.Name == webrtc.VP8 {
			log.Debugf("Got VP8 track, saving to disk as output.ivf")
			go func() {
				for {
					pkt, err := track.ReadRTP()
					if err != nil {
						if err == io.EOF {
							log.Debugf("All video frames reveived")
							peerConnection.Close()
							os.Exit(0)
							return
						}
						log.Panicf("Error track.ReadRTP: ", err)
					}

					//fmt.Printf("%s video, sequence=%d, timestamp=%d, payload=%d\n", time.Now().Format("15:04:05.000000"), pkt.SequenceNumber, pkt.Timestamp, len(pkt.Payload))
					if len(pkt.Payload) < 4 {
						log.Debugf("Ignore packet: payload is not large enough to ivf container header, %v\n", pkt)
						continue
					}
					if err := ivfFile.WriteRTP(pkt); err != nil {
						log.Panicf("Error write ivf: ", err)
					}
				}
			}()
		}
	})

	// Set the handler for ICE connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Debugf("Connection State has changed %s \n", connectionState.String())

		if connectionState == webrtc.ICEConnectionStateConnected {
			log.Debugf("Ctrl+C the remote client to stop the demo")
		} else if connectionState == webrtc.ICEConnectionStateFailed ||
			connectionState == webrtc.ICEConnectionStateDisconnected {
			closeErr := oggFile.Close()
			if closeErr != nil {
				log.Panicf("Error oggFile.Close: %v", closeErr)
			}

			closeErr = ivfFile.Close()
			if closeErr != nil {
				log.Panicf("Error ivfFile.Close: %v", closeErr)
			}

			log.Debugf("Done writing media files")
			os.Exit(0)
		}
	})

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		log.Panicf("Error creating offer: %v", err)
	}
	if err = peerConnection.SetLocalDescription(offer); err != nil {
		log.Panicf("Error setting local description: %v", err)
	}

	sid := os.Args[1]
	ctx := context.Background()
	client, err := c.Signal(ctx)

	if err != nil {
		log.Panicf("Error subscribing stream: %v", err)
	}

	err = client.Send(&sfu.SignalRequest{
		Payload: &sfu.SignalRequest_Join{
			Join: &sfu.JoinRequest{
				Sid: sid,
				Offer: &sfu.SessionDescription{
					Type: offer.Type.String(),
					Sdp:  []byte(peerConnection.LocalDescription().SDP),
				},
			},
		},
	})

	if err != nil {
		log.Panicf("Error sending subscribe request: %v", err)
	}

	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			// Gathering done
			return
		}
		bytes, err := json.Marshal(c.ToJSON())
		if err != nil {
			log.Errorf("OnIceCandidate error %s", err)
		}
		log.Debugf("send candidate:\n %s", string(bytes))
		err = client.Send(&sfu.SignalRequest{
			Payload: &sfu.SignalRequest_Trickle{
				Trickle: &sfu.Trickle{
					Init: string(bytes),
				},
			},
		})
		if err != nil {
			log.Errorf("OnIceCandidate error %s", err)
		}
	})

	// Handle sfu stream messages
	for {
		res, err := client.Recv()

		if err != nil {
			if err == io.EOF {
				// WebRTC Transport closed
				log.Debugf("WebRTC Transport Closed")
				err = client.CloseSend()
				if err != nil {
					log.Errorf("error sending close: %s", err)
				}
				return
			}

			errStatus, _ := status.FromError(err)
			if errStatus.Code() == codes.Canceled {
				err = client.CloseSend()
				if err != nil {
					log.Errorf("error sending close: %s", err)
				}
				return
			}

			log.Debugf("Error receiving signal response: %v", err)
			return
		}

		switch payload := res.Payload.(type) {
		case *sfu.SignalReply_Join:
			// Set the remote SessionDescription
			log.Debugf("got answer: %s", string(payload.Join.Answer.Sdp))
			if err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer,
				SDP:  string(payload.Join.Answer.Sdp),
			}); err != nil {
				log.Errorf("join error %s", err)
				return
			}

		case *sfu.SignalReply_Negotiate:
			log.Debugf("got negotiate %s", payload.Negotiate.Type)
			if payload.Negotiate.Type == webrtc.SDPTypeOffer.String() {
				log.Debugf("got offer: %s", string(payload.Negotiate.Sdp))
				offer := webrtc.SessionDescription{
					Type: webrtc.SDPTypeOffer,
					SDP:  string(payload.Negotiate.Sdp),
				}

				// Peer exists, renegotiating existing peer
				err = peerConnection.SetRemoteDescription(offer)
				if err != nil {
					log.Errorf("negotiate error %s", err)
					continue
				}

				var answer webrtc.SessionDescription
				answer, err = peerConnection.CreateAnswer(nil)
				if err != nil {
					log.Errorf("negotiate error %s", err)
					continue
				}

				err = peerConnection.SetLocalDescription(answer)
				if err != nil {
					log.Errorf("negotiate error %s", err)
					continue
				}

				err = client.Send(&sfu.SignalRequest{
					Payload: &sfu.SignalRequest_Negotiate{
						Negotiate: &sfu.SessionDescription{
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
				err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  string(payload.Negotiate.Sdp),
				})

				if err != nil {
					log.Errorf("negotiate error %s", err)
					continue
				}
			}
		case *sfu.SignalReply_Trickle:
			log.Debugf("got candidate: %v", string(payload.Trickle.Init))
			var candidate webrtc.ICECandidateInit
			_ = json.Unmarshal([]byte(payload.Trickle.Init), &candidate)
			err := peerConnection.AddICECandidate(candidate)
			if err != nil {
				log.Errorf("error adding ice candidate: %e", err)
			}
		}
	}
}
