package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
	"github.com/sourcegraph/jsonrpc2"
	websocketjsonrpc2 "github.com/sourcegraph/jsonrpc2/websocket"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/pion/ion-sfu/cmd/server/grpc/proto"
	sfu "github.com/pion/ion-sfu/pkg"
	"github.com/pion/ion-sfu/pkg/log"
)

const (
	portRangeLimit = 1024
)

var (
	file         string
	cert         string
	key          string
	gaddr, jaddr string

	conf      = Config{}
	errNoPeer = errors.New("no peer exists")
)

type grpcConfig struct {
	Port string `mapstructure:"port"`
}

// Config defines parameters for configuring the sfu instance
type Config struct {
	sfu.Config `mapstructure:",squash"`
	GRPC       grpcConfig `mapstructure:"grpc"`
}

// Join message sent when initializing a peer connection
type Join struct {
	Sid   string                    `json:"sid"`
	Offer webrtc.SessionDescription `json:"offer"`
}

// Negotiation message sent when renegotiating
type Negotiation struct {
	Desc webrtc.SessionDescription `json:"desc"`
}

// Trickle message sent when renegotiating
type Trickle struct {
	Candidate webrtc.ICECandidateInit `json:"candidate"`
}

func showHelp() {
	fmt.Printf("Usage:%s {params}\n", os.Args[0])
	fmt.Println("      -c {config file}")
	fmt.Println("      -cert {cert file used by jsonrpc}")
	fmt.Println("      -key {key file used by jsonrpc}")
	fmt.Println("      -gaddr {grpc listen addr}")
	fmt.Println("      -jaddr {jsonrpc listen addr}")
	fmt.Println("             {grpc and jsonrpc addrs should be set at least one}")
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
	flag.StringVar(&cert, "cert", "", "cert file")
	flag.StringVar(&key, "key", "", "key file")
	flag.StringVar(&jaddr, "jaddr", "", "jsonrpc listening address")
	flag.StringVar(&gaddr, "gaddr", "", "grpc listening address")
	help := flag.Bool("h", false, "help info")
	flag.Parse()

	//at least set one
	if gaddr == "" && jaddr == "" {
		return false
	}

	if !load() {
		return false
	}

	if *help {
		showHelp()
		return false
	}
	return true
}

type contextKey struct {
	name string
}
type peerContext struct {
	peer *sfu.WebRTCTransport
}

var peerCtxKey = &contextKey{"peer"}

func forContext(ctx context.Context) *peerContext {
	raw, _ := ctx.Value(peerCtxKey).(*peerContext)
	return raw
}

// Server can serve grpc and jsonrpc
type Server struct {
	sfu *sfu.SFU
}

// NewServer ...
func NewServer() *Server {
	return &Server{
		sfu: sfu.NewSFU(conf.Config),
	}
}

// ServeGRPC serve grpc
func (s *Server) ServeGRPC(gaddr string) error {
	l, err := net.Listen("tcp", gaddr)
	if err != nil {
		return err
	}

	gs := grpc.NewServer()
	pb.RegisterSFUService(gs, &pb.SFUService{
		Signal: s.GRPCSignal,
	})

	log.Infof("GRPC Listening at %s", gaddr)
	if err := gs.Serve(l); err != nil {
		log.Errorf("err=%v", err)
		return err
	}
	return nil
}

// ServeJSONRPC serve jsonrpc
func (s *Server) ServeJSONRPC(jaddr string) error {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	http.Handle("/ws", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Panicf("err=%v", err)
		}
		defer c.Close()

		p := &peerContext{}
		ctx := context.WithValue(r.Context(), peerCtxKey, p)
		jc := jsonrpc2.NewConn(ctx, websocketjsonrpc2.NewObjectStream(c), s)

		<-jc.DisconnectNotify()

		if p.peer != nil {
			log.Infof("Closing peer")
			p.peer.Close()
		}
	}))

	var err error
	if key != "" && cert != "" {
		log.Infof("JsonRPC Listening at https://[%s]", jaddr)
		err = http.ListenAndServeTLS(jaddr, cert, key, nil)
	} else {
		log.Infof("JsonRPC Listening at http://[%s]", jaddr)
		err = http.ListenAndServe(jaddr, nil)
	}
	if err != nil {
		log.Errorf("err=%v", err)
	}
	return err
}

// Handle Json-RPC
func (r *Server) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	p := forContext(ctx)

	var peerLock sync.Mutex
	switch req.Method {
	case "join":
		if p.peer != nil {
			log.Errorf("connect: peer already exists for connection")
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", errors.New("peer already exists")),
			})
			break
		}

		var join Join
		err := json.Unmarshal(*req.Params, &join)
		if err != nil {
			log.Errorf("connect: error parsing offer: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		me := sfu.MediaEngine{}
		err = me.PopulateFromSDP(join.Offer)
		if err != nil {
			log.Errorf("connect: error creating peer: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		peerLock.Lock()
		peer, err := r.sfu.NewWebRTCTransport(join.Sid, me)
		if err != nil {
			log.Errorf("connect: error creating peer: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			peerLock.Unlock()
			break
		}

		p.peer = peer
		// Notify user of trickle candidates
		peer.OnICECandidate(func(c *webrtc.ICECandidate) {
			if c == nil {
				// Gathering done
				return
			}

			log.Debugf("Sending ICE candidate %v", c.ToJSON())
			if err := conn.Notify(ctx, "trickle", c.ToJSON()); err != nil {
				log.Errorf("error sending trickle %s", err)
			}
		})

		peer.OnNegotiationNeeded(func() {
			log.Debugf("on negotiation needed called")
			offer, err := p.peer.CreateOffer()
			if err != nil {
				log.Errorf("CreateOffer error: %v", err)
				return
			}

			err = p.peer.SetLocalDescription(offer)
			if err != nil {
				log.Errorf("SetLocalDescription error: %v", err)
				return
			}

			if err := conn.Notify(ctx, "offer", offer); err != nil {
				log.Errorf("error sending offer %s", err)
			}
		})

		log.Infof("peer %s join session %s offer=%v", peer.ID(), join.Sid, join.Offer)

		err = peer.SetRemoteDescription(join.Offer)
		if err != nil {
			log.Errorf("Offer error: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			peerLock.Unlock()
			break
		}

		answer, err := peer.CreateAnswer()
		if err != nil {
			log.Errorf("Offer error: answer=%v err=%v", answer, err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			peerLock.Unlock()
			break
		}

		err = peer.SetLocalDescription(answer)
		if err != nil {
			log.Errorf("Offer error: answer=%v err=%v", answer, err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			peerLock.Unlock()
			break
		}

		_ = conn.Reply(ctx, req.ID, answer)
		peerLock.Unlock()

	case "offer":
		if p.peer == nil {
			log.Errorf("connect: no peer exists for connection")
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", errors.New("no peer exists")),
			})
			break
		}

		log.Infof("peer %s offer", p.peer.ID())

		var negotiation Negotiation
		err := json.Unmarshal(*req.Params, &negotiation)
		if err != nil {
			log.Errorf("connect: error parsing offer: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		// Peer exists, renegotiating existing peer
		err = p.peer.SetRemoteDescription(negotiation.Desc)
		if err != nil {
			log.Errorf("Offer error: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		answer, err := p.peer.CreateAnswer()
		if err != nil {
			log.Errorf("Offer error: answer=%v err=%v", answer, err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		err = p.peer.SetLocalDescription(answer)
		if err != nil {
			log.Errorf("Offer error: answer=%v err=%v", answer, err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		_ = conn.Reply(ctx, req.ID, answer)

	case "answer":
		if p.peer == nil {
			log.Errorf("connect: no peer exists for connection")
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", errors.New("no peer exists")),
			})
			break
		}

		log.Infof("peer %s answer", p.peer.ID())

		var negotiation Negotiation
		err := json.Unmarshal(*req.Params, &negotiation)
		if err != nil {
			log.Errorf("connect: error parsing answer: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		err = p.peer.SetRemoteDescription(negotiation.Desc)
		if err != nil {
			log.Errorf("error setting remote description %s", err)
		}

	case "trickle":
		peerLock.Lock()
		if p.peer == nil {
			log.Errorf("connect: no peer exists for connection")
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", errors.New("no peer exists")),
			})
			peerLock.Unlock()
			break
		}

		var trickle Trickle
		err := json.Unmarshal(*req.Params, &trickle)
		if err != nil {
			log.Errorf("connect: error parsing candidate: %v", err)
			_ = conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			peerLock.Unlock()
			break
		}
		log.Infof("peer %s AddICECandidate %v", p.peer.ID(), trickle.Candidate)

		err = p.peer.AddICECandidate(trickle.Candidate)
		if err != nil {
			log.Errorf("error setting ice candidate %s", err)
		}
		peerLock.Unlock()
	}
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
// If the webrtc connection is closed, the Server will close this stream.
//
// The client should send a message containing the session id
// and one of two different payload types:
// 1. `Connect` containing the session offer description. This
// message must *always* be sent first.
// 2. `Trickle` containing candidate information for Trickle ICE.
//
// If the client closes this stream, the webrtc stream will be closed.
func (s *Server) GRPCSignal(stream pb.SFU_SignalServer) error {
	var pid string
	var peer *sfu.WebRTCTransport
	var peerLock sync.Mutex
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
			err = me.PopulateFromSDP(offer)
			if err != nil {
				log.Errorf("join error: %v", err)
				return status.Errorf(codes.InvalidArgument, "join error %s", err)
			}

			peerLock.Lock()
			peer, err = s.sfu.NewWebRTCTransport(payload.Join.Sid, me)
			peerLock.Unlock()
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
					log.Debugf("peer.OnICECandidate done")
					return
				}
				bytes, err := json.Marshal(c.ToJSON())
				if err != nil {
					log.Errorf("OnIceCandidate error %s", err)
				}

				log.Debugf("peer.OnICECandidate %v", c.ToJSON())
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
			peerLock.Lock()
			if peer == nil {
				peerLock.Unlock()
				return status.Errorf(codes.FailedPrecondition, "%s", errNoPeer)
			}

			var candidate webrtc.ICECandidateInit
			err := json.Unmarshal([]byte(payload.Trickle.Init), &candidate)
			if err != nil {
				log.Errorf("error parsing ice candidate: %v", err)
			}

			log.Infof("peer %s AddICECandidate %v", peer.ID(), candidate)
			if err := peer.AddICECandidate(candidate); err != nil {
				peerLock.Unlock()
				return status.Errorf(codes.Internal, "error adding ice candidate")
			}
			peerLock.Unlock()
		}
	}
}

func main() {
	if !parse() {
		showHelp()
		os.Exit(-1)
	}
	log.Init(conf.Log.Level, conf.Log.Fix)
	log.Infof("--- Starting SFU Node ---")

	node := NewServer()

	if gaddr != "" {
		go node.ServeGRPC(gaddr)
	}

	if jaddr != "" {
		go node.ServeJSONRPC(jaddr)
	}

	select {}
}
