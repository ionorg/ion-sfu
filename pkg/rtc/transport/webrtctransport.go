package transport

import (
	"errors"
	"io"

	"sync"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/media"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v2"
)

const (
	maxChanSize = 100
)

var (
	errChanClosed     = errors.New("channel closed")
	errInvalidTrack   = errors.New("track is nil")
	errInvalidPacket  = errors.New("packet is nil")
	errInvalidPC      = errors.New("pc is nil")
	errPtNotSupported = errors.New("payloadtype not supported")
)

// WebRTCTransport contains pc incoming and outgoing tracks
type WebRTCTransport struct {
	id           string
	pc           *webrtc.PeerConnection
	me           *media.Engine
	outTracks    map[uint32]*webrtc.Track
	outTrackLock sync.RWMutex
	inTracks     map[uint32]*webrtc.Track
	inTrackLock  sync.RWMutex
	writeErrCnt  int

	rtpCh          chan *rtp.Packet
	rtcpCh         chan rtcp.Packet
	stop           bool
	bandwidth      uint32
	onCloseHandler func()
}

// NewWebRTCTransport create a WebRTCTransport
func NewWebRTCTransport(id string, pc *webrtc.PeerConnection, me *media.Engine) *WebRTCTransport {
	return &WebRTCTransport{
		id:        id,
		pc:        pc,
		me:        me,
		outTracks: make(map[uint32]*webrtc.Track),
		inTracks:  make(map[uint32]*webrtc.Track),
		rtpCh:     make(chan *rtp.Packet, maxChanSize),
		rtcpCh:    make(chan rtcp.Packet, maxChanSize),
	}
}

// ID return id
func (w *WebRTCTransport) ID() string {
	return w.id
}

// MediaEngine returns the webrtc peer connection media engine
func (w *WebRTCTransport) MediaEngine() *media.Engine {
	return w.me
}

// Type return type of transport
func (w *WebRTCTransport) Type() int {
	return TypeWebRTCTransport
}

// receiveInTrackRTP receive all incoming tracks' rtp and sent to one channel
func (w *WebRTCTransport) receiveInTrackRTP(remoteTrack *webrtc.Track) {
	for {
		if w.stop {
			return
		}

		rtp, err := remoteTrack.ReadRTP()
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Errorf("rtp err => %v", err)
		}
		w.rtpCh <- rtp
	}
}

// ReadRTP read rtp packet
func (w *WebRTCTransport) ReadRTP() (*rtp.Packet, error) {
	rtp, ok := <-w.rtpCh
	if !ok {
		return nil, errChanClosed
	}
	return rtp, nil
}

// WriteRTP send rtp packet to outgoing tracks
func (w *WebRTCTransport) WriteRTP(pkt *rtp.Packet) error {
	if pkt == nil {
		return errInvalidPacket
	}

	// Handle PT rewrites
	// If pub packet is not of paylod sub wants
	src := pkt.Header.PayloadType
	if pt, ok := w.me.MapTo(src); ok {
		// Do the transform
		newPkt := *pkt
		log.Tracef("Transforming %v => %v", src, pt)
		newPkt.Header.PayloadType = pt
		pkt = &newPkt
	}

	w.outTrackLock.RLock()
	track := w.outTracks[pkt.SSRC]
	w.outTrackLock.RUnlock()

	if track == nil {
		log.Errorf("WebRTCTransport.WriteRTP track==nil pkt.SSRC=%d PT=%d", pkt.SSRC, pkt.Header.PayloadType)
		return errInvalidTrack
	}

	log.Tracef("WebRTCTransport.WriteRTP pkt=%v", pkt)
	err := track.WriteRTP(pkt)
	if err != nil {
		log.Errorf("WebRTCTransport.WriteRTP => %s", err.Error())
		w.writeErrCnt++
		return err
	}
	return nil
}

// Close all
func (w *WebRTCTransport) Close() {
	if w.stop {
		return
	}
	w.stop = true
	log.Infof("WebRTCTransport.Close t.ID()=%v", w.ID())
	// close pc first, otherwise remoteTrack.ReadRTP will be blocked
	w.pc.Close()
	w.onCloseHandler()
}

// OnClose calls passed handler when closing pc
func (w *WebRTCTransport) OnClose(f func()) {
	w.onCloseHandler = f
}

// receive rtcp from outgoing track
func (w *WebRTCTransport) receiveOutTrackRTCP(sender *webrtc.RTPSender) {
	for {
		pkts, err := sender.ReadRTCP()
		if err == io.EOF || err == io.ErrClosedPipe {
			return
		}

		if err != nil {
			log.Errorf("rtcp err => %v", err)
		}

		if w.stop {
			return
		}

		for _, pkt := range pkts {
			w.rtcpCh <- pkt
		}
	}
}

// AddInTrack add an incoming track
func (w *WebRTCTransport) AddInTrack(track *webrtc.Track) {
	_, err := w.pc.AddTransceiver(track.Kind(), webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
	if err != nil {
		log.Errorf("AddInTrack error: pc.AddTransceiver %v", err)
		return
	}

	w.inTrackLock.Lock()
	w.inTracks[track.SSRC()] = track
	w.inTrackLock.Unlock()

	go w.receiveInTrackRTP(track)
}

// GetInTracks return incoming tracks
func (w *WebRTCTransport) GetInTracks() map[uint32]*webrtc.Track {
	w.inTrackLock.RLock()
	defer w.inTrackLock.RUnlock()
	return w.inTracks
}

// AddOutTrack add track to pc
func (w *WebRTCTransport) AddOutTrack(mid string, track *webrtc.Track) (*webrtc.Track, error) {
	if w.pc == nil {
		return nil, errInvalidPC
	}
	log.Debugf("AddOutTrack: %s %v", mid, track)

	pt, ok := w.me.MapTo(track.Codec().PayloadType)

	if !ok {
		return nil, errPtNotSupported
	}

	ssrc := track.SSRC()
	track, err := w.pc.NewTrack(pt, ssrc, mid, track.ID())
	if err != nil {
		return nil, err
	}

	t, err := w.pc.AddTransceiverFromTrack(track, webrtc.RtpTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
		SendEncodings: []webrtc.RTPEncodingParameters{{
			RTPCodingParameters: webrtc.RTPCodingParameters{SSRC: ssrc, PayloadType: pt},
		}},
	})

	if err != nil {
		return nil, err
	}

	w.outTrackLock.Lock()
	w.outTracks[ssrc] = track
	w.outTrackLock.Unlock()

	go w.receiveOutTrackRTCP(t.Sender())

	return track, nil
}

// WriteRTCP write rtcp packet to pc
func (w *WebRTCTransport) WriteRTCP(pkt rtcp.Packet) error {
	if w.pc == nil {
		return errInvalidPC
	}
	// log.Infof("WebRTCTransport.WriteRTCP pkt=%+v", pkt)
	return w.pc.WriteRTCP([]rtcp.Packet{pkt})
}

// WriteErrTotal return write error
func (w *WebRTCTransport) WriteErrTotal() int {
	return w.writeErrCnt
}

// WriteErrReset reset write error
func (w *WebRTCTransport) WriteErrReset() {
	w.writeErrCnt = 0
}

// GetRTCPChan return a rtcp channel
func (w *WebRTCTransport) GetRTCPChan() chan rtcp.Packet {
	return w.rtcpCh
}

// GetBandwidth return bandwidth
func (w *WebRTCTransport) GetBandwidth() uint32 {
	return w.bandwidth
}

// IsVideo check playload is video, now support chrome and firefox
func IsVideo(pt uint8) bool {
	if pt == webrtc.DefaultPayloadTypeVP8 ||
		pt == webrtc.DefaultPayloadTypeVP9 ||
		pt == webrtc.DefaultPayloadTypeH264 ||
		pt == 126 || pt == 97 {
		return true
	}
	return false
}
