package stats

import (
	"sync"

	"github.com/pion/ion-sfu/pkg/buffer"

	"github.com/pion/interceptor"

	log "github.com/pion/ion-log"
)

// Stream contains buffer with statistics
type Stream struct {
	sync.RWMutex
	Buffer *buffer.Buffer
	cname  string
}

// NewBuffer constructs a new Buffer
func NewStream(buffer *buffer.Buffer, _ *interceptor.StreamInfo) *Stream {
	s := &Stream{
		Buffer: buffer,
	}

	log.Debugf("NewStream")
	return s
}

func (s *Stream) GetCName() string {
	s.RLock()
	defer s.RUnlock()

	return s.cname
}

func (s *Stream) setCName(cname string) {
	s.Lock()
	defer s.Unlock()

	s.cname = cname
}
