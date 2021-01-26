package sfu

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bep/debounce"
	log "github.com/pion/ion-log"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

const APIChannelLabel = "ion-sfu"

type Subscriber struct {
	sync.RWMutex

	id string
	pc *webrtc.PeerConnection
	me *webrtc.MediaEngine

	tracks     map[string][]*DownTrack
	channels   map[string]*webrtc.DataChannel
	candidates []webrtc.ICECandidateInit

	negotiate func()

	closeOnce sync.Once
}

// NewSubscriber creates a new Subscriber
func NewSubscriber(id string, cfg WebRTCTransportConfig) (*Subscriber, error) {
	me, err := getSubscriberMediaEngine()
	if err != nil {
		log.Errorf("NewPeer error: %v", err)
		return nil, errPeerConnectionInitFailed
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(cfg.setting))
	pc, err := api.NewPeerConnection(cfg.configuration)

	if err != nil {
		log.Errorf("NewPeer error: %v", err)
		return nil, errPeerConnectionInitFailed
	}

	s := &Subscriber{
		id:       id,
		me:       me,
		pc:       pc,
		tracks:   make(map[string][]*DownTrack),
		channels: make(map[string]*webrtc.DataChannel),
	}

	pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Debugf("ice connection state: %s", connectionState)
		switch connectionState {
		case webrtc.ICEConnectionStateFailed:
			fallthrough
		case webrtc.ICEConnectionStateClosed:
			s.closeOnce.Do(func() {
				log.Debugf("webrtc ice closed for peer: %s", s.id)
				if err := s.Close(); err != nil {
					log.Errorf("webrtc transport close err: %v", err)
				}
			})
		}
	})

	go s.downTracksReports()

	return s, nil
}

func (s *Subscriber) AddDatachannel(peer *Peer, dc *Datachannel) error {
	ndc, err := s.pc.CreateDataChannel(dc.label, &webrtc.DataChannelInit{})
	if err != nil {
		return err
	}

	mws := newDCChain(dc.middlewares)
	p := mws.Process(ProcessFunc(func(ctx context.Context, args ProcessArgs) {
		if dc.onMessage != nil {
			dc.onMessage(ctx, args)
		}
	}))
	ndc.OnMessage(func(msg webrtc.DataChannelMessage) {
		p.Process(context.Background(), ProcessArgs{
			Peer:        peer,
			Message:     msg,
			DataChannel: ndc,
		})
	})

	s.channels[dc.label] = ndc

	return nil
}

func (s *Subscriber) OnNegotiationNeeded(f func()) {
	debounced := debounce.New(250 * time.Millisecond)
	s.negotiate = func() {
		debounced(f)
	}
}

func (s *Subscriber) CreateOffer() (webrtc.SessionDescription, error) {
	offer, err := s.pc.CreateOffer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}

	err = s.pc.SetLocalDescription(offer)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}

	return offer, nil
}

// OnICECandidate handler
func (s *Subscriber) OnICECandidate(f func(c *webrtc.ICECandidate)) {
	s.pc.OnICECandidate(f)
}

// AddICECandidate to peer connection
func (s *Subscriber) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	if s.pc.RemoteDescription() != nil {
		return s.pc.AddICECandidate(candidate)
	}
	s.candidates = append(s.candidates, candidate)
	return nil
}

func (s *Subscriber) AddDownTrack(streamID string, downTrack *DownTrack) {
	s.Lock()
	defer s.Unlock()
	if dt, ok := s.tracks[streamID]; ok {
		dt = append(dt, downTrack)
		s.tracks[streamID] = dt
	} else {
		s.tracks[streamID] = []*DownTrack{downTrack}
	}
}

func (s *Subscriber) RemoveDownTrack(streamID string, downTrack *DownTrack) {
	s.Lock()
	defer s.Unlock()
	if dts, ok := s.tracks[streamID]; ok {
		idx := -1
		for i, dt := range dts {
			if dt == downTrack {
				idx = i
			}
		}
		if idx >= 0 {
			dts[idx] = dts[len(dts)-1]
			dts[len(dts)-1] = nil
			dts = dts[:len(dts)-1]
			s.tracks[streamID] = dts
		}
	}
}

func (s *Subscriber) AddDataChannel(label string) (*webrtc.DataChannel, error) {
	s.Lock()
	defer s.Unlock()

	if s.channels[label] != nil {
		return s.channels[label], nil
	}

	dc, err := s.pc.CreateDataChannel(label, &webrtc.DataChannelInit{})
	if err != nil {
		log.Errorf("dc creation error: %v", err)
		return nil, errCreatingDataChannel
	}

	s.channels[label] = dc

	return dc, nil
}

// SetRemoteDescription sets the SessionDescription of the remote peer
func (s *Subscriber) SetRemoteDescription(desc webrtc.SessionDescription) error {
	if err := s.pc.SetRemoteDescription(desc); err != nil {
		log.Errorf("SetRemoteDescription error: %v", err)
		return err
	}

	for _, c := range s.candidates {
		if err := s.pc.AddICECandidate(c); err != nil {
			log.Errorf("Add subscriber ice candidate to peer %s err: %v", s.id, err)
		}
	}
	s.candidates = nil

	return nil
}

func (s *Subscriber) GetDownTracks(streamID string) []*DownTrack {
	s.RLock()
	defer s.RUnlock()
	return s.tracks[streamID]
}

// Close peer
func (s *Subscriber) Close() error {
	return s.pc.Close()
}

func (s *Subscriber) downTracksReports() {
	for {
		time.Sleep(5 * time.Second)

		if s.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
			return
		}

		var r []rtcp.Packet
		var sd []rtcp.SourceDescriptionChunk
		s.RLock()
		for _, dts := range s.tracks {
			for _, dt := range dts {
				if !dt.bound.get() {
					continue
				}
				now := time.Now().UnixNano()
				nowNTP := timeToNtp(now)
				lastPktMs := atomic.LoadInt64(&dt.lastPacketMs)
				maxPktTs := atomic.LoadUint32(&dt.lastTS)
				diffTs := uint32((now/1e6)-lastPktMs) * dt.codec.ClockRate / 1000
				octets, packets := dt.getSRStats()
				r = append(r, &rtcp.SenderReport{
					SSRC:        dt.ssrc,
					NTPTime:     nowNTP,
					RTPTime:     maxPktTs + diffTs,
					PacketCount: packets,
					OctetCount:  octets,
				})
				sd = append(sd, rtcp.SourceDescriptionChunk{
					Source: dt.ssrc,
					Items: []rtcp.SourceDescriptionItem{{
						Type: rtcp.SDESCNAME,
						Text: dt.streamID,
					}},
				}, rtcp.SourceDescriptionChunk{
					Source: dt.ssrc,
					Items: []rtcp.SourceDescriptionItem{{
						Type: rtcp.SDESType(15),
						Text: dt.transceiver.Mid(),
					}},
				})
			}
		}
		s.RUnlock()
		i := 0
		j := 0
		for i < len(sd) {
			i = (j + 1) * 15
			if i >= len(sd) {
				i = len(sd)
			}
			nsd := sd[j*15 : i]
			r = append(r, &rtcp.SourceDescription{Chunks: nsd})
			j++
			if err := s.pc.WriteRTCP(r); err != nil {
				if err == io.EOF || err == io.ErrClosedPipe {
					return
				}
				log.Errorf("Sending downtrack reports err: %v", err)
			}
			r = r[:0]
		}
	}
}

func (s *Subscriber) sendStreamDownTracksReports(streamID string) {
	var r []rtcp.Packet
	var sd []rtcp.SourceDescriptionChunk

	s.RLock()
	dts := s.tracks[streamID]
	for _, dt := range dts {
		if !dt.bound.get() {
			continue
		}
		sd = append(sd, rtcp.SourceDescriptionChunk{
			Source: dt.ssrc,
			Items: []rtcp.SourceDescriptionItem{{
				Type: rtcp.SDESCNAME,
				Text: dt.streamID,
			}},
		}, rtcp.SourceDescriptionChunk{
			Source: dt.ssrc,
			Items: []rtcp.SourceDescriptionItem{{
				Type: rtcp.SDESType(15),
				Text: dt.transceiver.Mid(),
			}},
		})
	}
	s.RUnlock()
	r = append(r, &rtcp.SourceDescription{Chunks: sd})
	go func() {
		r := r
		i := 0
		for {
			if err := s.pc.WriteRTCP(r); err != nil {
				log.Errorf("Sending track binding reports err:%v", err)
			}
			if i > 5 {
				return
			}
			i++
			time.Sleep(20 * time.Millisecond)
		}
	}()
}
