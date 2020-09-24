// Package pub-from-disk-using-grpc contains an example of publishing a stream to
// an ion-sfu instance from a file on disk.
package main

import (
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"os"
	"time"

	sfu "github.com/pion/ion-sfu/cmd/server/grpc/proto"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/ivfreader"
	"github.com/pion/webrtc/v3/pkg/media/oggreader"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	address       = "localhost:50051"
	audioFileName = "output.ogg"
	videoFileName = "output.ivf"
)

func main() {
	log.Init("debug", []string{"proc.go", "asm_amd64.s", "jsonrpc2.go"})

	// Assert that we have an audio or video file
	_, err := os.Stat(videoFileName)
	haveVideoFile := !os.IsNotExist(err)

	_, err = os.Stat(audioFileName)
	haveAudioFile := !os.IsNotExist(err)

	if !haveAudioFile && !haveVideoFile {
		log.Panicf("Could not find `" + audioFileName + "` or `" + videoFileName + "`")
	}

	// Set up a connection to the sfu server.
	conn, err := grpc.Dial(address, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Panicf("did not connect: %s", err)
	}
	defer conn.Close()
	c := sfu.NewSFUClient(conn)

	// We make our own mediaEngine so we can place the sender's codecs in it.  This because we must use the
	// dynamic media type from the sender in our answer. This is not required if we are the offerer
	mediaEngine := webrtc.MediaEngine{}
	mediaEngine.RegisterDefaultCodecs()

	// Create a new RTCPeerConnection
	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine))
	peerConnection, err := api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	})
	if err != nil {
		log.Panicf("Error new peerconnection: %s\n", err)
	}
	iceConnectedCtx, iceConnectedCtxCancel := context.WithCancel(context.Background())

	if haveVideoFile {
		// Create a video track
		videoTrack, addTrackErr := peerConnection.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), "video", "pion")
		if addTrackErr != nil {
			log.Panicf("Error new video track: %s\n", addTrackErr)
		}
		if _, addTrackErr = peerConnection.AddTrack(videoTrack); err != nil {
			log.Panicf("Error add video track: %s\n", addTrackErr)
		}

		go func() {
			// Open a IVF file and start reading using our IVFReader
			file, ivfErr := os.Open(videoFileName)
			if ivfErr != nil {
				log.Panicf("Error open video file %s\n", videoFileName, ivfErr)
			}

			ivf, header, ivfErr := ivfreader.NewWith(file)
			if ivfErr != nil {
				log.Panicf("Error new ivfreader: %s\n", ivfErr)
			}

			// Wait for connection established
			<-iceConnectedCtx.Done()

			// Send our video file frame at a time. Pace our sending so we send it at the same speed it should be played back as.
			// This isn't required since the video is timestamped, but we will such much higher loss if we send all at once.
			sleepTime := time.Millisecond * time.Duration((float32(header.TimebaseNumerator)/float32(header.TimebaseDenominator))*1000)
			for {
				frame, _, ivfErr := ivf.ParseNextFrame()
				if ivfErr == io.EOF {
					log.Debugf("All video frames parsed and sent")
					peerConnection.Close()
					os.Exit(0)
				}

				if ivfErr != nil {
					log.Panicf("Error ivf parse next frame %s\n", ivfErr)
				}

				time.Sleep(sleepTime)
				if ivfErr = videoTrack.WriteSample(media.Sample{Data: frame, Samples: 90000}); ivfErr != nil {
					log.Panicf("Error video track write sample: %s\n", ivfErr)
				}
				// packets := videoTrack.Packetizer().Packetize(frame, 90000)
				// for _, p := range packets {
				// 	fmt.Printf("%s sequence=%d, timestamp=%d, payload=%d\n", time.Now().Format("15:04:05.000000"), p.SequenceNumber, p.Timestamp, len(p.Payload))
				// 	err := videoTrack.WriteRTP(p)
				// 	if err != nil {
				// 		log.Panicf("Error video track write rtp: %s\n", err)
				// 	}
				// }
			}
		}()
	}

	if haveAudioFile {
		// Create a audio track
		audioTrack, addTrackErr := peerConnection.NewTrack(webrtc.DefaultPayloadTypeOpus, rand.Uint32(), "audio", "pion")
		if addTrackErr != nil {
			log.Panicf("Error new audio track: %s\n", addTrackErr)
		}
		if _, addTrackErr = peerConnection.AddTrack(audioTrack); err != nil {
			log.Panicf("Error add audio track: %s\n", addTrackErr)
		}

		go func() {
			// Open a IVF file and start reading using our IVFReader
			file, oggErr := os.Open(audioFileName)
			if oggErr != nil {
				log.Panicf("Error open audio file %s\n", audioFileName, oggErr)
			}

			// Open on oggfile in non-checksum mode.
			ogg, _, oggErr := oggreader.NewWith(file)
			if oggErr != nil {
				log.Panicf("Error new oggreader: %s\n", oggErr)
			}

			// Wait for connection established
			<-iceConnectedCtx.Done()

			// Keep track of last granule, the difference is the amount of samples in the buffer
			var lastGranule uint64
			for {
				pageData, pageHeader, oggErr := ogg.ParseNextPage()
				if oggErr == io.EOF {
					log.Debugf("All audio pages parsed and sent")
					peerConnection.Close()
					os.Exit(0)
				}

				if oggErr != nil {
					log.Panicf("Error ogg parse next page: %s\n", oggErr)
				}

				// The amount of samples is the difference between the last and current timestamp
				sampleCount := float64((pageHeader.GranulePosition - lastGranule))
				lastGranule = pageHeader.GranulePosition
				if oggErr = audioTrack.WriteSample(media.Sample{Data: pageData, Samples: uint32(sampleCount)}); oggErr != nil {
					log.Panicf("Error audio track write sample: %s\n", oggErr)
				}

				// Convert seconds to Milliseconds, Sleep doesn't accept floats
				time.Sleep(time.Duration((sampleCount/48000)*1000) * time.Millisecond)
			}
		}()
	}

	// Set the handler for ICE connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Debugf("Connection State has changed %s \n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateConnected {
			iceConnectedCtxCancel()
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
		log.Panicf("Error publishing stream: %v", err)
	}

	log.Debugf("send offer:\n %s", offer.SDP)
	err = client.Send(
		&sfu.SignalRequest{
			Payload: &sfu.SignalRequest_Join{
				Join: &sfu.JoinRequest{
					Sid: sid,
					Offer: &sfu.SessionDescription{
						Type: offer.Type.String(),
						Sdp:  []byte(offer.SDP),
					},
				},
			},
		},
	)

	if err != nil {
		log.Panicf("Error sending publish request: %v", err)
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
