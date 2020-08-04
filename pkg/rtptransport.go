package sfu

import (
	"errors"
	"net"

	"github.com/lucsky/cuid"
	"github.com/pion/ion-sfu/pkg/log"
	"github.com/pion/ion-sfu/pkg/rtpengine/muxrtp"
	"github.com/pion/ion-sfu/pkg/rtpengine/muxrtp/mux"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

const (
	receiveMTU    = 1500
	maxRTPPktSize = 1024
)

var (
	errInvalidConn = errors.New("invalid conn")
)

// RTPTransport ..
type RTPTransport struct {
	rtpSession   *muxrtp.SessionRTP
	rtcpSession  *muxrtp.SessionRTCP
	rtpEndpoint  *mux.Endpoint
	rtcpEndpoint *mux.Endpoint
	conn         net.Conn
	mux          *mux.Mux
	rtpCh        chan *rtp.Packet
	stop         bool
	id           string
	rtcpCh       chan rtcp.Packet
}

// NewRTPTransport create a RTPTransport by net.Conn
func NewRTPTransport(conn net.Conn) *RTPTransport {
	if conn == nil {
		log.Errorf("NewRTPTransport err=%v", errInvalidConn)
		return nil
	}
	t := &RTPTransport{
		id:     cuid.New(),
		conn:   conn,
		rtpCh:  make(chan *rtp.Packet, maxRTPPktSize),
		rtcpCh: make(chan rtcp.Packet, maxRTPPktSize),
	}
	config := mux.Config{
		Conn:       conn,
		BufferSize: receiveMTU,
	}
	t.mux = mux.NewMux(config)
	t.rtpEndpoint = t.mux.NewEndpoint(mux.MatchRTP)
	t.rtcpEndpoint = t.mux.NewEndpoint(mux.MatchRTCP)
	var err error
	t.rtpSession, err = muxrtp.NewSessionRTP(t.rtpEndpoint)
	if err != nil {
		log.Errorf("muxrtp.NewSessionRTP => %s", err.Error())
		return nil
	}
	t.rtcpSession, err = muxrtp.NewSessionRTCP(t.rtcpEndpoint)
	if err != nil {
		log.Errorf("muxrtp.NewSessionRTCP => %s", err.Error())
		return nil
	}
	t.receiveRTP()
	return t
}

// ID return id
func (r *RTPTransport) ID() string {
	return r.id
}

// Close release all
func (r *RTPTransport) Close() {
	if r.stop {
		return
	}
	log.Infof("RTPTransport.Close()")
	r.stop = true
	r.rtpSession.Close()
	r.rtcpSession.Close()
	r.rtpEndpoint.Close()
	r.rtcpEndpoint.Close()
	r.mux.Close()
	r.conn.Close()
}

// ReceiveRTP receive rtp
func (r *RTPTransport) receiveRTP() {
	go func() {
		for {
			if r.stop {
				break
			}
			readStream, ssrc, err := r.rtpSession.AcceptStream()
			if err == muxrtp.ErrSessionRTPClosed {
				r.Close()
				return
			} else if err != nil {
				log.Warnf("Failed to accept stream %v ", err)
				//for non-blocking ReadRTP()
				r.rtpCh <- nil
				continue
			}
			go func() {
				for {
					if r.stop {
						return
					}
					rtpBuf := make([]byte, receiveMTU)
					_, pkt, err := readStream.ReadRTP(rtpBuf)
					if err != nil {
						log.Warnf("Failed to read rtp %v %d ", err, ssrc)
						//for non-blocking ReadRTP()
						r.rtpCh <- nil
						continue
						// return
					}

					log.Debugf("RTPTransport.receiveRTP pkt=%v", pkt)

					r.rtpCh <- pkt
				}
			}()
		}
	}()
}

// ReadRTP read rtp from transport
func (r *RTPTransport) ReadRTP() (*rtp.Packet, error) {
	return <-r.rtpCh, nil
}

// rtp sub receive rtcp
func (r *RTPTransport) receiveRTCP() {
	go func() {
		for {
			if r.stop {
				break
			}
			readStream, ssrc, err := r.rtcpSession.AcceptStream()
			if err == muxrtp.ErrSessionRTCPClosed {
				return
			} else if err != nil {
				log.Warnf("Failed to accept RTCP %v ", err)
				return
			}

			go func() {
				rtcpBuf := make([]byte, receiveMTU)
				for {
					if r.stop {
						return
					}
					rtcps, err := readStream.ReadRTCP(rtcpBuf)
					if err != nil {
						log.Warnf("Failed to read rtcp %v %d ", err, ssrc)
						return
					}
					log.Debugf("got RTCPs: %+v ", rtcps)
					for _, pkt := range rtcps {
						switch pkt.(type) {
						case *rtcp.PictureLossIndication:
							log.Debugf("got pli, not need send key frame!")
						case *rtcp.TransportLayerNack:
							log.Debugf("rtptransport got nack: %+v", pkt)
							r.rtcpCh <- pkt
						}
					}
				}
			}()
		}
	}()
}

// WriteRTP send rtp packet
func (r *RTPTransport) WriteRTP(rtp *rtp.Packet) error {
	log.Debugf("RTPTransport.WriteRTP rtp=%v", rtp)
	writeStream, err := r.rtpSession.OpenWriteStream()
	if err != nil {
		log.Errorf("write error %+v", err)
		return err
	}

	_, err = writeStream.WriteRTP(&rtp.Header, rtp.Payload)

	if err != nil {
		log.Errorf("writeStream.WriteRTP => %s", err.Error())
	}
	return err
}

// WriteRTCP write rtcp
func (r *RTPTransport) WriteRTCP(pkt rtcp.Packet) error {
	bin, err := pkt.Marshal()
	if err != nil {
		return err
	}
	writeStream, err := r.rtcpSession.OpenWriteStream()
	if err != nil {
		return err
	}
	_, err = writeStream.WriteRawRTCP(bin)
	if err != nil {
		return err
	}
	return err
}

// RemoteAddr return remote addr
func (r *RTPTransport) RemoteAddr() net.Addr {
	if r.conn == nil {
		log.Errorf("RemoteAddr err=%v", errInvalidConn)
		return nil
	}
	return r.conn.RemoteAddr()
}

func (r *RTPTransport) stats() string {
	return ""
}
