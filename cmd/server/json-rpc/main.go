// Package cmd contains an entrypoint for running an ion-sfu instance.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/gorilla/websocket"
	sfu "github.com/pion/ion-sfu/pkg"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/webrtc/v2"
	"github.com/sourcegraph/jsonrpc2"
	websocketjsonrpc2 "github.com/sourcegraph/jsonrpc2/websocket"
	"github.com/spf13/viper"
)

var (
	conf = sfu.Config{}
	file string
)

const (
	portRangeLimit = 100
)

func showHelp() {
	fmt.Printf("Usage:%s {params}\n", os.Args[0])
	fmt.Println("      -c {config file}")
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

	if len(conf.WebRTC.ICEPortRange) != 0 && conf.WebRTC.ICEPortRange[1]-conf.WebRTC.ICEPortRange[0] <= portRangeLimit {
		fmt.Printf("config file %s loaded failed. range port must be [min, max] and max - min >= %d\n", file, portRangeLimit)
		return false
	}

	fmt.Printf("config %s load ok!\n", file)
	return true
}

func parse() bool {
	flag.StringVar(&file, "c", "config.toml", "config file")
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

type contextKey struct {
	name string
}
type peerContext struct {
	peer *sfu.Peer
}

var peerCtxKey = &contextKey{"peer"}

func forContext(ctx context.Context) *peerContext {
	raw, _ := ctx.Value(peerCtxKey).(*peerContext)
	return raw
}

// RPC defines the json-rpc
type RPC struct {
	sfu *sfu.SFU
}

// NewRPC ...
func NewRPC() *RPC {
	return &RPC{
		sfu: sfu.NewSFU(conf),
	}
}

// Handle RPC call
func (r *RPC) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	p := forContext(ctx)

	switch req.Method {
	case "offer":
		log.Infof("offer")

		var offer webrtc.SessionDescription
		err := json.Unmarshal(*req.Params, &offer)
		if err != nil {
			log.Errorf("connect: error parsing offer: %v", err)
			conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		// If no peer exists, create one
		if p.peer == nil {
			peer, answer, err := r.sfu.CreatePeer(offer)

			if err != nil {
				log.Errorf("connect: error creating peer: %v", err)
				conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
					Code:    500,
					Message: fmt.Sprintf("%s", err),
				})
				break
			}

			// Notify user of trickle candidates
			peer.OnICECandidate(func(c *webrtc.ICECandidate) {
				log.Infof("Sending ice candidate")
				if err := conn.Notify(ctx, "trickle", c.String()); err != nil {
					log.Errorf("error sending trickle %s", err)
				}
			})

			// New track router added to peer
			peer.OnRouter(func(router *sfu.Router) {
				// offer, err := p.peer.CreateOffer()
				// if err != nil {
				// 	log.Errorf("OnTrack error: %v", offer, err)
				// 	return
				// }

				// err = p.peer.SetLocalDescription(offer)
				// if err != nil {
				// 	log.Errorf("OnTrack error: %v", offer, err)
				// 	return
				// }
				// conn.Notify(ctx, "offer", offer)
			})

			p.peer = peer

			conn.Reply(ctx, req.ID, answer)
		} else {
			// Peer exists, renegotiating existing peer
			err := p.peer.SetRemoteDescription(offer)
			if err != nil {
				log.Errorf("Offer error: %v", err)
				conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
					Code:    500,
					Message: fmt.Sprintf("%s", err),
				})
				break
			}

			answer, err := p.peer.CreateAnswer()
			if err != nil {
				log.Errorf("Offer error: answer=%v err=%v", answer, err)
				conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
					Code:    500,
					Message: fmt.Sprintf("%s", err),
				})
				break
			}

			err = p.peer.SetLocalDescription(answer)
			if err != nil {
				log.Errorf("Offer error: answer=%v err=%v", answer, err)
				conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
					Code:    500,
					Message: fmt.Sprintf("%s", err),
				})
				break
			}

			conn.Reply(ctx, req.ID, answer)
		}

	case "answer":
		log.Infof("answer")
		if p.peer == nil {
			log.Errorf("connect: no peer exists for connection")
			conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", errors.New("no peer exists")),
			})
			break
		}

		var answer webrtc.SessionDescription
		err := json.Unmarshal(*req.Params, &answer)
		if err != nil {
			log.Errorf("connect: error parsing answer: %v", err)
			conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		p.peer.SetRemoteDescription(answer)

	case "trickle":
		log.Infof("trickle")
		if p.peer == nil {
			log.Errorf("connect: no peer exists for connection")
			conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", errors.New("no peer exists")),
			})
			break
		}

		var candidate webrtc.ICECandidateInit
		err := json.Unmarshal(*req.Params, &candidate)
		if err != nil {
			log.Errorf("connect: error parsing candidate: %v", err)
			conn.ReplyWithError(ctx, req.ID, &jsonrpc2.Error{
				Code:    500,
				Message: fmt.Sprintf("%s", err),
			})
			break
		}

		p.peer.AddICECandidate(candidate)
	}
}

func main() {
	if !parse() {
		showHelp()
		os.Exit(-1)
	}

	log.Infof("--- Starting SFU Node ---")
	rpc := NewRPC()
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
			panic(err)
		}
		defer c.Close()

		p := &peerContext{}
		ctx := context.WithValue(r.Context(), peerCtxKey, p)
		jc := jsonrpc2.NewConn(ctx, websocketjsonrpc2.NewObjectStream(c), rpc)
		<-jc.DisconnectNotify()
	}))

	log.Infof("Listening at %s", "localhost:7000")
	err := http.ListenAndServe("localhost:7000", nil)
	if err != nil {
		panic(err)
	}
}
