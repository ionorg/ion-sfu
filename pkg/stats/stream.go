package stats

import (
	"sync"
	"sync/atomic"

	"github.com/pion/ion-sfu/pkg/buffer"

	"github.com/pion/interceptor"

	log "github.com/pion/ion-log"
)

// Stream contains buffer statistics
type Stream struct {
	sync.RWMutex
	Buffer        *buffer.Buffer
	cname         string
	driftInMillis uint64
	hasStats      bool
	lastStats     buffer.BufferStats
	diffStats     buffer.BufferStats
}

// NewStream constructs a new Stream
func NewStream(buffer *buffer.Buffer, _ *interceptor.StreamInfo) *Stream {
	s := &Stream{
		Buffer: buffer,
	}

	log.Debugf("NewStream")
	return s
}

// GetCName returns the cname for a given stream
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

func (s *Stream) setDriftInMillis(driftInMillis uint64) {
	atomic.StoreUint64(&s.driftInMillis, driftInMillis)
}

func (s *Stream) getDriftInMillis() uint64 {
	return atomic.LoadUint64(&s.driftInMillis)
}

func (s *Stream) updateStats(stats buffer.BufferStats) (hasDiff bool, diffStats buffer.BufferStats) {
	s.Lock()
	defer s.Unlock()

	hadStats := false

	if s.hasStats {
		s.diffStats.LastExpected = stats.LastExpected - s.lastStats.LastExpected
		s.diffStats.LastReceived = stats.LastReceived - s.lastStats.LastReceived
		s.diffStats.PacketCount = stats.PacketCount - s.lastStats.PacketCount
		s.diffStats.TotalByte = stats.TotalByte - s.lastStats.TotalByte
		hadStats = true
	}

	s.lastStats = stats
	s.hasStats = true

	return hadStats, s.diffStats
}
