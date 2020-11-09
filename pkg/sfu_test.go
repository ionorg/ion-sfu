package sfu

import (
	"math/rand"
	"sync"
	"testing"
	"time"

	log "github.com/pion/ion-log"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/assert"
)

// Init test helpers

func signalPair(pcOffer *webrtc.PeerConnection, pcAnswer *webrtc.PeerConnection) error {
	offer, err := pcOffer.CreateOffer(nil)
	if err != nil {
		return err
	}
	gatherComplete := webrtc.GatheringCompletePromise(pcOffer)
	if err = pcOffer.SetLocalDescription(offer); err != nil {
		return err
	}
	<-gatherComplete
	if err = pcAnswer.SetRemoteDescription(*pcOffer.LocalDescription()); err != nil {
		return err
	}

	answer, err := pcAnswer.CreateAnswer(nil)
	if err != nil {
		return err
	}
	if err = pcAnswer.SetLocalDescription(answer); err != nil {
		return err
	}
	return pcOffer.SetRemoteDescription(*pcAnswer.LocalDescription())
}

func sendRTPWithSenderUntilDone(done <-chan struct{}, t *testing.T, track *webrtc.Track, sender Sender) {
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			pkt := track.Packetizer().Packetize([]byte{0x01, 0x02, 0x03, 0x04}, 1)[0]
			sender.WriteRTP(pkt)
		case <-done:
			return
		}
	}
}

func sendRTPUntilDone(start, done <-chan struct{}, t *testing.T, track *webrtc.Track) {
	<-start
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			pkt := track.Packetizer().Packetize([]byte{0x05, 0x06, 0x07, 0x08}, 1)[0]
			pkt.Payload = []byte{0xff, 0xff, 0xff, 0xfd, 0xb4, 0x9f, 0x94, 0x1}
			_ = track.WriteRTP(pkt)
		case <-done:
			return
		}
	}
}

// newPair creates two new peer connections (an offerer and an answerer) using
// the api.
func newPair(cfg webrtc.Configuration, api *webrtc.API) (pcOffer *webrtc.PeerConnection, pcAnswer *webrtc.PeerConnection, err error) {
	pca, err := api.NewPeerConnection(cfg)
	if err != nil {
		return nil, nil, err
	}

	pcb, err := api.NewPeerConnection(cfg)
	if err != nil {
		return nil, nil, err
	}

	return pca, pcb, nil
}

type media struct {
	kind string
	id   string
	tid  string
}

type action struct {
	id    string
	kind  string
	sleep time.Duration
	media []media
}

type peer struct {
	id     string
	mu     sync.Mutex
	local  *Peer
	remote *webrtc.PeerConnection
	subs   sync.WaitGroup
	pubs   []*sender
}

type step struct {
	actions []*action
}

type sender struct {
	transceiver *webrtc.RTPTransceiver
	start       chan struct{}
}

func addMedia(done <-chan struct{}, t *testing.T, pc *webrtc.PeerConnection, media []media) []*sender {
	var senders []*sender
	for _, media := range media {
		var track *webrtc.Track
		var err error

		start := make(chan struct{})

		switch media.kind {
		case "audio":
			track, err = pc.NewTrack(webrtc.DefaultPayloadTypeOpus, rand.Uint32(), media.tid, media.id)
			assert.NoError(t, err)
			transceiver, err := pc.AddTransceiverFromTrack(track, webrtc.RtpTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionSendonly,
			})
			assert.NoError(t, err)
			senders = append(senders, &sender{transceiver: transceiver, start: start})
		case "video":
			track, err = pc.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), media.tid, media.id)
			assert.NoError(t, err)
			transceiver, err := pc.AddTransceiverFromTrack(track, webrtc.RtpTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionSendonly,
			})
			assert.NoError(t, err)
			senders = append(senders, &sender{transceiver: transceiver, start: start})
		}

		go sendRTPUntilDone(start, done, t, track)
	}
	return senders
}

func TestSFU_SessionScenarios(t *testing.T) {
	fixByFile := []string{"asm_amd64.s", "proc.go", "icegatherer.go", "jsonrpc2"}
	fixByFunc := []string{"Handle"}
	log.Init("trace", fixByFile, fixByFunc)
	sfu := NewSFU(Config{Log: log.Config{Level: "trace"}})

	tests := []struct {
		name  string
		steps []step
	}{
		{
			name: "Sequential join",
			steps: []step{
				{
					actions: []*action{{
						id:   "remote1",
						kind: "join",
					}},
				},
				{
					actions: []*action{{
						id:   "remote1",
						kind: "publish",
						media: []media{
							{kind: "audio", id: "stream1", tid: "audio"},
							{kind: "video", id: "stream1", tid: "video"},
						},
					}},
				},
				{
					actions: []*action{{
						id:   "remote2",
						kind: "join",
					}},
				},
				{
					actions: []*action{{
						id:   "remote2",
						kind: "publish",
						media: []media{
							{kind: "audio", id: "stream2", tid: "audio"},
							{kind: "video", id: "stream2", tid: "video"},
						},
					}},
				},
			},
		},
		{
			name: "Concurrent join + publish",
			steps: []step{
				{
					actions: []*action{{
						id:   "remote1",
						kind: "join",
					}, {
						id:   "remote2",
						kind: "join",
					}, {
						id:   "remote3",
						kind: "join",
					}},
				},
				{
					actions: []*action{{
						id:   "remote1",
						kind: "publish",
						media: []media{
							{kind: "audio", id: "stream1", tid: "audio"},
							{kind: "video", id: "stream1", tid: "video"},
						},
					}, {
						id:   "remote2",
						kind: "publish",
						media: []media{
							{kind: "audio", id: "stream2", tid: "audio"},
							{kind: "video", id: "stream2", tid: "video"},
						},
					}, {
						id:   "remote3",
						kind: "publish",
						media: []media{
							{kind: "audio", id: "stream3", tid: "audio"},
							{kind: "video", id: "stream3", tid: "video"},
						},
					}},
				},
			},
		},
		{
			name: "Multiple stream publish",
			steps: []step{
				{
					actions: []*action{{
						id:   "remote1",
						kind: "join",
					}, {
						id:   "remote2",
						kind: "join",
					}},
				},
				{
					actions: []*action{{
						id:   "remote1",
						kind: "publish",
						media: []media{
							{kind: "audio", id: "stream1", tid: "audio1"},
							{kind: "video", id: "stream1", tid: "video1"},
						},
					}},
				}, {
					actions: []*action{{
						id:   "remote2",
						kind: "publish",
						media: []media{
							{kind: "audio", id: "stream2", tid: "audio2"},
							{kind: "video", id: "stream2", tid: "video2"},
						},
					}},
				},
				{
					actions: []*action{{
						id:   "remote1",
						kind: "publish",
						media: []media{
							{kind: "audio", id: "stream3", tid: "audio3"},
							{kind: "video", id: "stream3", tid: "video3"},
						},
					}},
				},
				{
					actions: []*action{{
						id:   "remote1",
						kind: "unpublish",
						media: []media{
							{kind: "audio", id: "stream1", tid: "audio1"},
							{kind: "video", id: "stream1", tid: "video1"},
						},
					}},
				}, {
					actions: []*action{{
						id:   "remote2",
						kind: "publish",
						media: []media{
							{kind: "audio", id: "stream4", tid: "audio4"},
							{kind: "video", id: "stream4", tid: "video4"},
						},
					}},
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			testDone := atomicBool{}
			var mu sync.RWMutex
			done := make(chan struct{})
			peers := make(map[string]*peer)

			for _, step := range tt.steps {
				for _, action := range step.actions {
					func() {
						switch action.kind {
						case "join":
							me := webrtc.MediaEngine{}
							me.RegisterDefaultCodecs()
							api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
							r, err := api.NewPeerConnection(webrtc.Configuration{})

							assert.NoError(t, err)
							_, err = r.CreateDataChannel("ion-sfu", nil)
							assert.NoError(t, err)
							local := NewPeer(sfu)
							p := &peer{id: action.id, remote: r, local: &local}
							r.OnTrack(func(track *webrtc.Track, recv *webrtc.RTPReceiver) {
								mu.Lock()
								p.subs.Done()
								mu.Unlock()
							})

							mu.Lock()
							for id, existing := range peers {
								if id != action.id {
									p.subs.Add(len(existing.pubs))
								}
							}
							peers[action.id] = p
							mu.Unlock()

							p.mu.Lock()
							p.remote.OnNegotiationNeeded(func() {
								p.mu.Lock()
								defer p.mu.Unlock()
								o, err := p.remote.CreateOffer(nil)
								assert.NoError(t, err)
								err = p.remote.SetLocalDescription(o)
								assert.NoError(t, err)
								a, err := p.local.Answer(o)
								assert.NoError(t, err)
								p.remote.SetRemoteDescription(*a)

								for _, pub := range p.pubs {
									if pub.start != nil {
										close(pub.start)
										pub.start = nil
									}
								}
							})

							p.local.OnOffer = func(o *webrtc.SessionDescription) {
								if testDone.get() {
									return
								}
								p.mu.Lock()
								defer p.mu.Unlock()
								err := p.remote.SetRemoteDescription(*o)
								assert.NoError(t, err)
								a, err := p.remote.CreateAnswer(nil)
								assert.NoError(t, err)
								err = p.remote.SetLocalDescription(a)
								assert.NoError(t, err)
								go func() {
									if testDone.get() {
										return
									}
									err = p.local.SetRemoteDescription(a)
									assert.NoError(t, err)
								}()
							}

							offer, err := p.remote.CreateOffer(nil)
							assert.NoError(t, err)
							gatherComplete := webrtc.GatheringCompletePromise(p.remote)
							err = p.remote.SetLocalDescription(offer)
							assert.NoError(t, err)
							<-gatherComplete
							answer, err := p.local.Join("test", *p.remote.LocalDescription())
							assert.NoError(t, err)
							p.remote.SetRemoteDescription(*answer)
							p.mu.Unlock()

						case "publish":
							mu.Lock()
							peer := peers[action.id]
							peer.mu.Lock()
							// all other peers should get sub'd
							for id, p := range peers {
								if id != peer.id {
									p.subs.Add(len(action.media))
								}
							}

							peer.pubs = append(peer.pubs, addMedia(done, t, peer.remote, action.media)...)
							peer.mu.Unlock()
							mu.Unlock()

						case "unpublish":
							mu.Lock()
							peer := peers[action.id]
							peer.mu.Lock()
							for _, media := range action.media {
								for _, pub := range peer.pubs {
									if pub.transceiver != nil && pub.transceiver.Sender().Track().ID() == media.tid {
										peer.remote.RemoveTrack(pub.transceiver.Sender())
										pub.transceiver = nil
									}
								}
							}
							peer.mu.Unlock()
							mu.Unlock()
						}
					}()
					time.Sleep(1 * time.Second)
				}
			}

			for _, p := range peers {
				p.subs.Wait()
			}
			testDone.set(true)
			close(done)

			for _, p := range peers {
				p.mu.Lock()
				p.remote.Close()
				p.local.Close()
				p.mu.Unlock()
			}
		})
	}
}
