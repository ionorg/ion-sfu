package rtc

const (
	maxWriteErr = 100
)

type RouterConfig struct {
	MinBandwidth uint64 `mapstructure:"minbandwidth"`
	MaxBandwidth uint64 `mapstructure:"maxbandwidth"`
	REMBFeedback bool   `mapstructure:"rembfeedback"`
}

//                        +--->sub
//                        |
// pub--->pubCh-->subCh---+--->sub
//                        |
//                        +--->sub
// Router is rtp router
// type Router struct {
// 	id             string
// 	pub            transport.Transport
// 	subs           map[string]transport.SendTrack
// 	subLock        sync.RWMutex
// 	stop           bool
// 	subChans       map[string]chan *rtp.Packet
// 	rembChan       chan *rtcp.ReceiverEstimatedMaximumBitrate
// 	onCloseHandler func()
// }

// NewRouter return a new Router
// func NewRouter(id string) *Router {
// 	log.Infof("NewRouter id=%s", id)
// 	return &Router{
// 		id:       id,
// 		subs:     make(map[string]transport.SendTrack),
// 		subChans: make(map[string]chan *rtp.Packet),
// 		rembChan: make(chan *rtcp.ReceiverEstimatedMaximumBitrate),
// 	}
// }

// func (r *Router) start() {
// 	if routerConfig.REMBFeedback {
// 		go r.rembLoop()
// 	}
// 	go func() {
// 		defer util.Recover("[Router.start]")
// 		for {
// 			if r.stop {
// 				return
// 			}

// 			var pkt *rtp.Packet
// 			var err error
// 			// get rtp from pub
// 			pkt, err = r.pub.ReadRTP()
// 			if err != nil {
// 				log.Errorf("r.pub.ReadRTP err=%v", err)
// 				continue
// 			}
// 			// log.Debugf("pkt := <-r.subCh %v", pkt)
// 			if pkt == nil {
// 				continue
// 			}
// 			r.subLock.RLock()
// 			// Push to client send queues
// 			for i := range r.GetSubs() {
// 				// Nonblock sending
// 				select {
// 				case r.subChans[i] <- pkt:
// 				default:
// 					log.Errorf("Sub consumer is backed up. Dropping packet")
// 				}
// 			}
// 			r.subLock.RUnlock()
// 		}
// 	}()
// }

// // delPub
// func (r *Router) delPub() {
// 	log.Infof("Router.delPub %s", r.pub.ID())
// 	if r.pub != nil {
// 		r.pub.Close()
// 	}
// 	r.pub = nil
// }

// // GetPub get pub
// func (r *Router) GetPub() transport.Transport {
// 	// log.Infof("Router.GetPub %v", r.pub)
// 	return r.pub
// }

// func (r *Router) subWriteLoop(subID string, trans transport.Transport) {
// 	for pkt := range r.subChans[subID] {
// 		// log.Infof(" WriteRTP %v:%v to %v PT: %v", pkt.SSRC, pkt.SequenceNumber, trans.ID(), pkt.Header.PayloadType)

// 		if err := trans.WriteRTP(pkt); err != nil {
// 			// log.Errorf("wt.WriteRTP err=%v", err)
// 			// del sub when err is increasing
// 			if trans.WriteErrTotal() > maxWriteErr {
// 				r.delSub(trans.ID())
// 			}
// 		}
// 		trans.WriteErrReset()
// 	}
// 	log.Infof("Closing sub writer")
// }

// AddSub add a sub to router
// func (r *Router) AddSub(id string, t transport.Transport) transport.Transport {
// 	//fix panic: assignment to entry in nil map
// 	if r.stop {
// 		return nil
// 	}
// 	r.subLock.Lock()
// 	defer r.subLock.Unlock()
// 	r.subs[id] = t
// 	r.subChans[id] = make(chan *rtp.Packet, 1000)
// 	log.Infof("Router.AddSub id=%s t=%p", id, t)

// 	t.OnClose(func() {
// 		r.delSub(id)
// 	})

// 	// Sub loops
// 	go r.subWriteLoop(id, t)
// 	go r.subFeedbackLoop(id, t)
// 	return t
// }

// // GetSub get a sub by id
// func (r *Router) GetSub(id string) transport.Transport {
// 	r.subLock.RLock()
// 	defer r.subLock.RUnlock()
// 	// log.Infof("Router.GetSub id=%s sub=%v", id, r.subs[id])
// 	return r.subs[id]
// }

// // GetSubs get all subs
// func (r *Router) GetSubs() map[string]transport.Transport {
// 	r.subLock.RLock()
// 	defer r.subLock.RUnlock()
// 	// log.Infof("Router.GetSubs len=%v", len(r.subs))
// 	return r.subs
// }

// // delSub del sub by id
// func (r *Router) delSub(id string) {
// 	log.Infof("Router.delSub id=%s", id)
// 	r.subLock.Lock()
// 	defer r.subLock.Unlock()
// 	if r.subs[id] != nil {
// 		r.subs[id].Close()
// 	}
// 	if r.subChans[id] != nil {
// 		close(r.subChans[id])
// 	}
// 	delete(r.subs, id)
// 	delete(r.subChans, id)
// }

// // delSubs del all sub
// func (r *Router) delSubs() {
// 	log.Infof("Router.delSubs")
// 	r.subLock.RLock()
// 	keys := make([]string, 0, len(r.subs))
// 	for k := range r.subs {
// 		keys = append(keys, k)
// 	}
// 	r.subLock.RUnlock()

// 	for _, id := range keys {
// 		r.delSub(id)
// 	}
// }

// // Close release all
// func (r *Router) Close() {
// 	if r.stop {
// 		return
// 	}
// 	log.Infof("Router.Close")
// 	r.onCloseHandler()
// 	r.delPub()
// 	r.stop = true
// 	r.delSubs()
// }

// // OnClose handler called when router is closed.
// func (r *Router) OnClose(f func()) {
// 	r.onCloseHandler = f
// }

// func (r *Router) resendRTP(sid string, ssrc uint32, sn uint16) bool {
// 	if r.pub == nil {
// 		return false
// 	}
// 	hd := r.pluginChain.GetPlugin(plugins.TypeJitterBuffer)
// 	if hd != nil {
// 		jb := hd.(*plugins.JitterBuffer)
// 		pkt := jb.GetPacket(ssrc, sn)
// 		if pkt == nil {
// 			// log.Infof("Router.resendRTP pkt not found sid=%s ssrc=%d sn=%d pkt=%v", sid, ssrc, sn, pkt)
// 			return false
// 		}
// 		sub := r.GetSub(sid)
// 		if sub != nil {
// 			err := sub.WriteRTP(pkt)
// 			if err != nil {
// 				log.Errorf("router.resendRTP err=%v", err)
// 			}
// 			// log.Infof("Router.resendRTP sid=%s ssrc=%d sn=%d", sid, ssrc, sn)
// 			return true
// 		}
// 	}
// 	return false
// }
