package sfu

import (
	"sort"
	"sync"
)

type audioStream struct {
	id    string
	sum   int
	total int
}

type audioLevel struct {
	sync.RWMutex
	threshold uint8
	streams   []*audioStream
}

func newAudioLevel(threshold uint8) *audioLevel {
	if threshold > 127 {
		threshold = 127
	}
	return &audioLevel{
		threshold: threshold,
	}
}

func (a *audioLevel) addStream(streamID string) {
	a.Lock()
	a.streams = append(a.streams, &audioStream{id: streamID})
	a.Unlock()
}

func (a *audioLevel) removeStream(streamID string) {
	a.Lock()
	defer a.Unlock()
	idx := -1
	for i, s := range a.streams {
		if s.id == streamID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return
	}
	a.streams[idx] = a.streams[len(a.streams)-1]
	a.streams[len(a.streams)-1] = nil
	a.streams = a.streams[:len(a.streams)-1]
}

func (a *audioLevel) observe(streamID string, dBov uint8) {
	a.RLock()
	defer a.RUnlock()
	for _, as := range a.streams {
		if as.id == streamID {
			if dBov <= a.threshold {
				as.sum += int(dBov)
				as.total += 1
			}
			return
		}
	}
}

func (a *audioLevel) calc() []string {
	a.Lock()
	defer a.Unlock()

	sort.Slice(a.streams, func(i, j int) bool {
		si, sj := a.streams[i], a.streams[j]
		switch {
		case si.total != sj.total:
			return si.total > sj.total
		default:
			return si.sum < sj.sum
		}
	})

	streamIDs := make([]string, len(a.streams))
	for idx, s := range a.streams {
		if s.total > 0 {
			streamIDs[idx] = s.id
		}
		s.total = 0
		s.sum = 0
	}

	return streamIDs
}
