package sfu

//go:generate go run github.com/matryer/moq -out router_mock_test.generated.go . Router

import (
	"math/rand"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pion/sdp/v3"

	"github.com/pion/webrtc/v3"

	log "github.com/pion/ion-log"
	"github.com/pion/rtcp"
)

const (
	SimpleReceiver = iota + 1
	SimulcastReceiver
	SVCReceiver
)

// Router defines a track rtp/rtcp router
type Router interface {
	ID() string
	Config() RouterConfig
	AddReceiver(track *webrtc.Track, receiver *webrtc.RTPReceiver) *receiverRouter
	AddSender(p *WebRTCTransport, rr *receiverRouter) error
	SetExtMap(pd *sdp.SessionDescription)
	OfferExtMap() map[webrtc.SDPSectionType][]sdp.ExtMap
	GetExtMap(mid, ext string) uint8
	SendRTCP(pkts []rtcp.Packet)
	Stop()
}

// RouterConfig defines router configurations
type RouterConfig struct {
	MaxBandwidth  uint64          `mapstructure:"maxbandwidth"`
	MaxBufferTime int             `mapstructure:"maxbuffertime"`
	Simulcast     SimulcastConfig `mapstructure:"simulcast"`
}

type receiverRouter struct {
	kind      int
	stream    string
	receivers [3]Receiver
}

type router struct {
	id         string
	mu         sync.RWMutex
	peer       *webrtc.PeerConnection
	twcc       *TransportWideCC
	rtcpCh     chan []rtcp.Packet
	config     RouterConfig
	receivers  map[string]*receiverRouter
	extensions map[webrtc.SDPSectionType][]sdp.ExtMap
}

// newRouter for routing rtp/rtcp packets
func newRouter(peer *webrtc.PeerConnection, id string, config RouterConfig) Router {
	ch := make(chan []rtcp.Packet, 10)
	r := &router{
		id:         id,
		peer:       peer,
		twcc:       newTransportWideCC(ch),
		config:     config,
		rtcpCh:     ch,
		receivers:  make(map[string]*receiverRouter),
		extensions: make(map[webrtc.SDPSectionType][]sdp.ExtMap),
	}
	go r.sendRTCP()
	return r
}

func (r *router) ID() string {
	return r.id
}

func (r *router) Config() RouterConfig {
	return r.config
}

func (r *router) AddReceiver(track *webrtc.Track, receiver *webrtc.RTPReceiver) *receiverRouter {
	r.mu.Lock()
	defer r.mu.Unlock()

	var mid string
	for _, t := range r.peer.GetTransceivers() {
		if t.Receiver() == receiver {
			mid = t.Mid()
		}
	}
	trackID := track.ID()

	var twccExt uint8
	if e, ok := r.extensions[webrtc.SDPSectionType(mid)]; ok {
		for _, ex := range e {
			if ex.URI.String() == sdp.TransportCCURI {
				twccExt = uint8(ex.Value)
			}
		}
	}

	recv := NewWebRTCReceiver(receiver, track, BufferOptions{
		BufferTime: r.config.MaxBufferTime,
		MaxBitRate: r.config.MaxBandwidth * 1000,
		TWCCExt:    twccExt,
	})
	recv.OnTransportWideCC(func(sn uint16, timeNS int64, marker bool) {
		r.twcc.push(sn, timeNS, marker)
	})
	recv.SetRTCPCh(r.rtcpCh)
	recv.OnCloseHandler(func() {
		r.deleteReceiver(trackID)
	})
	if track.Kind() == webrtc.RTPCodecTypeVideo {
		r.twcc.mSSRC = track.SSRC()
		r.twcc.tccLastReport = time.Now().UnixNano()
	}
	recv.Start()

	if rr, ok := r.receivers[trackID]; ok {
		rr.receivers[recv.SpatialLayer()] = recv
		return nil
	}

	rr := &receiverRouter{
		stream:    track.Label(),
		receivers: [3]Receiver{},
	}
	rr.receivers[recv.SpatialLayer()] = recv

	if len(track.RID()) > 0 {
		rr.kind = SimulcastReceiver
	} else {
		rr.kind = SimpleReceiver
	}

	r.receivers[trackID] = rr
	return rr
}

// AddWebRTCSender to router
func (r *router) AddSender(p *WebRTCTransport, rr *receiverRouter) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if rr != nil {
		if err := r.addSender(p, rr); err != nil {
			return err
		}
		p.negotiate()
		return nil
	}

	if len(r.receivers) > 0 {
		for _, rr = range r.receivers {
			if err := r.addSender(p, rr); err != nil {
				return err
			}
		}
		p.negotiate()
	}
	return nil
}

func (r *router) SendRTCP(pkts []rtcp.Packet) {
	r.rtcpCh <- pkts
}

func (r *router) Stop() {
	close(r.rtcpCh)
}

func (r *router) addSender(p *WebRTCTransport, rr *receiverRouter) error {
	var (
		recv   Receiver
		sender Sender
		ssrc   uint32
	)

	if rr.kind == SimpleReceiver {
		recv = rr.receivers[0]
		ssrc = recv.Track().SSRC()
	} else {
		for _, rcv := range rr.receivers {
			if rcv != nil {
				recv = rcv
			}
			if !r.config.Simulcast.BestQualityFirst && rcv != nil {
				break
			}
		}
		ssrc = rand.Uint32()
	}

	if recv == nil {
		return errNoReceiverFound
	}

	for _, s := range p.GetSenders(rr.stream) {
		if s.Track().ID() == recv.Track().ID() {
			return nil
		}
	}

	inTrack := recv.Track()
	to := p.me.GetCodecsByName(recv.Track().Codec().Name)
	if len(to) == 0 {
		return errPtNotSupported
	}
	pt := to[0].PayloadType
	outTrack, err := p.pc.NewTrack(pt, ssrc, inTrack.ID(), inTrack.Label())
	if err != nil {
		return err
	}
	// Create webrtc sender for the peer we are sending track to
	t, err := p.pc.AddTransceiverFromTrack(outTrack, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
	})
	if err != nil {
		return err
	}
	if rr.kind == SimulcastReceiver {
		sender = NewSimulcastSender(p.id, rr, t, recv.SpatialLayer(), r.config.Simulcast)
	} else {
		sender = NewSimpleSender(p.id, rr, t)
	}
	sender.OnCloseHandler(func() {
		if err := p.pc.RemoveTrack(t.Sender()); err != nil { // nolint:scopelint
			log.Errorf("Error closing sender: %s", err) // nolint:scopelint
		} else {
			p.negotiate() // nolint:scopelint
		}
	})
	p.mu.Lock()
	p.pendingSenders.PushBack(pendingSender{
		transceiver: t,
		sender:      sender,
	})
	p.mu.Unlock()
	p.AddSender(rr.stream, sender)
	recv.AddSender(sender)
	return nil
}

func (r *router) deleteReceiver(track string) {
	r.mu.Lock()
	delete(r.receivers, track)
	r.mu.Unlock()
}

func (r *router) sendRTCP() {
	for pkts := range r.rtcpCh {
		if err := r.peer.WriteRTCP(pkts); err != nil {
			log.Errorf("Write rtcp to peer %s err :%v", r.id, err)
		}
	}
}

func (r *router) GetExtMap(mid, ext string) uint8 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.extensions[webrtc.SDPSectionType(mid)]; ok {
		for _, ex := range e {
			if ex.URI.String() == ext {
				return uint8(ex.Value)
			}
		}
	}
	return 0
}

// nolint:scopelint
func (r *router) SetExtMap(pd *sdp.SessionDescription) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, md := range pd.MediaDescriptions {
		if md.MediaName.Media != mediaNameAudio && md.MediaName.Media != mediaNameVideo {
			continue
		}

		mid, ok := md.Attribute(sdp.AttrKeyMID)
		if !ok {
			continue
		}
		if _, ok := r.extensions[webrtc.SDPSectionType(mid)]; ok {
			continue
		}

		addExt := func(att sdp.Attribute) error {
			em := sdp.ExtMap{}
			if err := em.Unmarshal("extmap:" + att.Value); err != nil {
				log.Errorf("Parsing ext map err: %v", err)
				return err
			}
			r.extensions[webrtc.SDPSectionType(mid)] = append(r.extensions[webrtc.SDPSectionType(mid)], em)
			return nil
		}

		var enterExtMap bool
		for _, att := range md.Attributes {
			isExtMap := false
			if att.Key == sdp.AttrKeyExtMap {
				enterExtMap = true
				isExtMap = true
				if strings.HasSuffix(att.Value, sdp.TransportCCURI) {
					if err := addExt(att); err != nil {
						continue
					}
				}
				if strings.HasSuffix(att.Value, sdp.SDESMidURI) {
					if err := addExt(att); err != nil {
						continue
					}
				}
				if strings.HasSuffix(att.Value, sdp.SDESRTPStreamIDURI) {
					if err := addExt(att); err != nil {
						continue
					}
				}
			}
			if enterExtMap && !isExtMap {
				break
			}
		}

	}
}

// nolint:scopelint
func (r *router) OfferExtMap() map[webrtc.SDPSectionType][]sdp.ExtMap {
	r.mu.Lock()
	defer r.mu.Unlock()

	sdesMid, _ := url.Parse(sdp.SDESMidURI)

	for _, t := range r.peer.GetTransceivers() {
		if _, ok := r.extensions[webrtc.SDPSectionType(t.Mid())]; !ok {
			switch t.Kind() {
			case webrtc.RTPCodecTypeAudio:
				if t.Direction() == webrtc.RTPTransceiverDirectionSendonly {
					r.extensions[webrtc.SDPSectionType(t.Mid())] = []sdp.ExtMap{
						{
							Value: 1,
							URI:   sdesMid,
						},
					}
				}
			case webrtc.RTPCodecTypeVideo:
				if t.Direction() == webrtc.RTPTransceiverDirectionSendonly {
					r.extensions[webrtc.SDPSectionType(t.Mid())] = []sdp.ExtMap{
						{
							Value: 1,
							URI:   sdesMid,
						},
					}
				}
			}
		}
	}

	return r.extensions
}
