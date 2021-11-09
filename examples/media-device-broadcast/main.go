package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/codec/vpx"
	"github.com/pion/mediadevices/pkg/frame"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/webrtc/v3"
	"github.com/sourcegraph/jsonrpc2"

	// Note: If you don't have a camera or microphone or your adapters are not supported,
	//       you can always swap your adapters with our dummy adapters below.
	// _ "github.com/pion/mediadevices/pkg/driver/audiotest"
	// _ "github.com/pion/mediadevices/pkg/driver/videotest"
	// These are required to register camera and microphone adapter
	_ "github.com/pion/mediadevices/pkg/driver/camera"
	_ "github.com/pion/mediadevices/pkg/driver/microphone"
)

type Candidate struct {
	Target    int                  `json:"target"`
	Candidate *webrtc.ICECandidate `json:"candidate"`
}

type ResponseCandidate struct {
	Target    int                      `json:"target"`
	Candidate *webrtc.ICECandidateInit `json:"candidate"`
}

// SendOffer object to send to the sfu over Websockets
type SendOffer struct {
	SID   string                     `json:"sid"`
	Offer *webrtc.SessionDescription `json:"offer"`
}

// SendAnswer object to send to the sfu over Websockets
type SendAnswer struct {
	SID    string                     `json:"sid"`
	Answer *webrtc.SessionDescription `json:"answer"`
}

// TrickleResponse received from the sfu server
type TrickleResponse struct {
	Params ResponseCandidate `json:"params"`
	Method string            `json:"method"`
}

// Response received from the sfu over Websockets
type Response struct {
	Params *webrtc.SessionDescription `json:"params"`
	Result *webrtc.SessionDescription `json:"result"`
	Method string                     `json:"method"`
	Id     uint64                     `json:"id"`
}

var peerConnection *webrtc.PeerConnection
var connectionID uint64

var addr string

func main() {
	flag.StringVar(&addr, "addr", "localhost:7000", "address to use")
	flag.Parse()

	u := url.URL{Scheme: "ws", Host: addr, Path: "/ws"}
	log.Printf("connecting to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
		SDPSemantics: webrtc.SDPSemanticsUnifiedPlanWithFallback,
	}

	// MediaEngine lets us define the codecs supported by the peer connection
	mediaEngine := webrtc.MediaEngine{}

	vpxParams, err := vpx.NewVP8Params()
	if err != nil {
		panic(err)
	}
	vpxParams.BitRate = 500_000 // 500kbps

	codecSelector := mediadevices.NewCodecSelector(
		mediadevices.WithVideoEncoders(&vpxParams),
	)

	codecSelector.Populate(&mediaEngine)
	api := webrtc.NewAPI(webrtc.WithMediaEngine(&mediaEngine))
	peerConnection, err = api.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}

	// Read incoming Websocket messages
	done := make(chan struct{})

	go readMessage(c, done)

	// print all connected devices
	fmt.Println(mediadevices.EnumerateDevices())

	// get the user media
	s, err := mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			c.FrameFormat = prop.FrameFormat(frame.FormatYUY2)
			c.Width = prop.Int(640)
			c.Height = prop.Int(480)
		},
		Codec: codecSelector,
	})

	if err != nil {
		panic(err)
	}

	for _, track := range s.GetTracks() {
		track.OnEnded(func(err error) {
			fmt.Printf("Track (ID: %s) ended with error: %v\n",
				track.ID(), err)
		})
		_, err = peerConnection.AddTransceiverFromTrack(track,
			webrtc.RtpTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionSendonly,
			},
		)
		if err != nil {
			panic(err)
		}
	}

	// Creating WebRTC offer
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}

	// Set the remote SessionDescription
	err = peerConnection.SetLocalDescription(offer)
	if err != nil {
		panic(err)
	}

	// Handling OnICECandidate event because we need to correctly respond
	// to the WebRTC events and the response from the Websockets server
	// is called whenever a new ICE candidate is found. The method is then
	// used to negotiate a connection with the remote peer by sending
	// a trickle request to the sfu
	peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}

		candidateJSON, err := json.Marshal(&Candidate{
			Candidate: candidate,
			Target:    0,
		})

		params := (*json.RawMessage)(&candidateJSON)

		if err != nil {
			log.Fatal(err)
		}

		message := &jsonrpc2.Request{
			Method: "trickle",
			Params: params,
		}

		reqBodyBytes := new(bytes.Buffer)
		json.NewEncoder(reqBodyBytes).Encode(message)

		messageBytes := reqBodyBytes.Bytes()
		c.WriteMessage(websocket.TextMessage, messageBytes)

	})

	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Printf("Connection State has changed to %s \n", connectionState.String())
	})

	offerJSON, err := json.Marshal(&SendOffer{
		Offer: peerConnection.LocalDescription(),
		SID:   "test room",
	})
	if err != nil {
		panic(err)
	}

	params := (*json.RawMessage)(&offerJSON)

	connectionUUID := uuid.New()
	connectionID = uint64(connectionUUID.ID())

	offerMessage := &jsonrpc2.Request{
		Method: "join",
		Params: params,
		ID: jsonrpc2.ID{
			IsString: false,
			Str:      "",
			Num:      connectionID,
		},
	}

	reqBodyBytes := new(bytes.Buffer)
	json.NewEncoder(reqBodyBytes).Encode(offerMessage)

	// send the offer over to the sfu using Websockets
	messageBytes := reqBodyBytes.Bytes()
	c.WriteMessage(websocket.TextMessage, messageBytes)

	<-done
}

// readMessage is used to receive and react to the incoming
// Websockets messages send by the sfu
func readMessage(connection *websocket.Conn, done chan struct{}) {
	defer close(done)

	for {
		_, message, err := connection.ReadMessage()
		if err != nil || err == io.EOF {
			log.Fatal("Error reading: ", err)
			break
		}

		fmt.Printf("recv: %s", message)

		var response Response
		json.Unmarshal(message, &response)

		// determine which event the message is for and handle them accordingly
		if response.Id == connectionID {
			result := *response.Result
			if err := peerConnection.SetRemoteDescription(result); err != nil {
				log.Fatal(err)
			}
		} else if response.Id != 0 && response.Method == "offer" {
			// the sfu sends an offer and we react by saving the send offer into the remote
			// description of our peer connection and sending back an answer with the
			// local description so we can connect to the remote peer.

			peerConnection.SetRemoteDescription(*response.Params)
			answer, err := peerConnection.CreateAnswer(nil)
			if err != nil {
				log.Fatal(err)
			}

			peerConnection.SetLocalDescription(answer)

			connectionUUID := uuid.New()
			connectionID = uint64(connectionUUID.ID())

			offerJSON, err := json.Marshal(&SendAnswer{
				Answer: peerConnection.LocalDescription(),
				SID:    "test room",
			})
			if err != nil {
				log.Fatal(err)
			}

			params := (*json.RawMessage)(&offerJSON)

			answerMessage := &jsonrpc2.Request{
				Method: "answer",
				Params: params,
				ID: jsonrpc2.ID{
					IsString: false,
					Str:      "",
					Num:      connectionID,
				},
			}

			reqBodyBytes := new(bytes.Buffer)
			json.NewEncoder(reqBodyBytes).Encode(answerMessage)

			messageBytes := reqBodyBytes.Bytes()
			connection.WriteMessage(websocket.TextMessage, messageBytes)
		} else if response.Method == "trickle" {
			// The sfu sends a new ICE candidate and we add it to the peer connection

			var trickleResponse TrickleResponse

			if err := json.Unmarshal(message, &trickleResponse); err != nil {
				log.Fatal(err)
			}

			err := peerConnection.AddICECandidate(*trickleResponse.Params.Candidate)

			if err != nil {
				log.Fatal(err)
			}
		}
	}
}
