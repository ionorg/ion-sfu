package sfu

import (
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/lucsky/cuid"
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

func sendRTPUntilDone(done <-chan struct{}, t *testing.T, track *webrtc.Track) {
	for {
		select {
		case <-time.After(20 * time.Millisecond):
			pkt := track.Packetizer().Packetize([]byte{0x05, 0x06, 0x07, 0x08}, 1)[0]
			pkt.Payload = []byte{0xff, 0xff, 0xff, 0xfd, 0xb4, 0x9f, 0x94, 0x1}
			err := track.WriteRTP(pkt)
			assert.NoError(t, err)
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

type remote struct {
	id     string
	action string
	media  []media
}

type peer struct {
	id     string
	mu     sync.Mutex
	local  *Peer
	remote *webrtc.PeerConnection
	subs   sync.WaitGroup
	pubs   []media
}

type step struct {
	remotes []*remote
}

func addMedia(done <-chan struct{}, t *testing.T, pc *webrtc.PeerConnection, media []media) {
	for _, media := range media {
		var track *webrtc.Track
		var err error

		switch media.kind {
		case "audio":
			track, err = pc.NewTrack(webrtc.DefaultPayloadTypeOpus, rand.Uint32(), media.tid, media.id)
			assert.NoError(t, err)
			_, err = pc.AddTrack(track)
			assert.NoError(t, err)
		case "video":
			track, err = pc.NewTrack(webrtc.DefaultPayloadTypeVP8, rand.Uint32(), media.tid, media.id)
			assert.NoError(t, err)
			_, err = pc.AddTrack(track)
			assert.NoError(t, err)
		}

		go sendRTPUntilDone(done, t, track)
	}
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
					remotes: []*remote{{
						id:     "remote1",
						action: "join",
					}},
				},
				{
					remotes: []*remote{{
						id:     "remote1",
						action: "publish",
						media: []media{
							{kind: "audio", id: "stream1", tid: cuid.New()},
							{kind: "video", id: "stream1", tid: cuid.New()},
						},
					}},
				},
				{
					remotes: []*remote{{
						id:     "remote2",
						action: "join",
					}},
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan struct{})

			peers := make(map[string]*peer)
			for _, step := range tt.steps {
				for _, remote := range step.remotes {
					p := peers[remote.id]

					switch remote.action {
					case "join":
						assert.Nil(t, p)

						me := webrtc.MediaEngine{}
						me.RegisterDefaultCodecs()
						api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
						r, err := api.NewPeerConnection(webrtc.Configuration{})
						r.OnTrack(func(*webrtc.Track, *webrtc.RTPReceiver) {
							p.subs.Done()
						})
						assert.NoError(t, err)
						_, err = r.CreateDataChannel("ion-sfu", nil)
						assert.NoError(t, err)
						local := NewPeer(sfu)
						p = &peer{id: remote.id, remote: r, local: &local}

						for id, existing := range peers {
							if id != remote.id {
								p.subs.Add(len(existing.pubs))
							}
						}

						peers[remote.id] = p

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
						})

						p.local.OnOffer = func(o *webrtc.SessionDescription) {
							p.mu.Lock()
							defer p.mu.Unlock()
							err := p.remote.SetRemoteDescription(*o)
							assert.NoError(t, err)
							a, err := p.remote.CreateAnswer(nil)
							assert.NoError(t, err)
							err = p.remote.SetLocalDescription(a)
							assert.NoError(t, err)
							err = p.local.SetRemoteDescription(a)
							assert.NoError(t, err)
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
						p.pubs = append(p.pubs, remote.media...)

						// all other peers should get sub'd
						for id, p := range peers {
							if id != p.id {
								p.subs.Add(len(remote.media))
							}
						}

						addMedia(done, t, p.remote, remote.media)
					}
				}
			}

			for _, p := range peers {
				p.subs.Wait()
			}
			close(done)
		})
	}
}
