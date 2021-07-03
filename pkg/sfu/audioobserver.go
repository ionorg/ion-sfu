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

type AudioObserver struct {
	sync.Mutex
	streams   map[string]*audioStream
	expected  int
	threshold uint8
	previous  []string
}

func NewAudioObserver(threshold uint8, interval, filter int) *AudioObserver {
	if threshold > 127 {
		threshold = 127
	}
	if filter < 0 {
		filter = 0
	}
	if filter > 100 {
		filter = 100
	}

	return &AudioObserver{
		streams:   make(map[string]*audioStream),
		threshold: threshold,
		expected:  interval * filter / 2000,
	}
}

func (a *AudioObserver) addStream(streamID string) {
	stream := &audioStream{id: streamID}
	a.Lock()
	a.streams[streamID] = stream
	a.Unlock()
}

func (a *AudioObserver) removeStream(streamID string) {
	a.Lock()
	delete(a.streams, streamID)
	a.Unlock()
}

func (a *AudioObserver) observe(streamID string, dBov uint8) {
	if dBov > a.threshold {
		return
	}
	a.Lock()
	if stream := a.streams[streamID]; stream != nil {
		stream.sum += int(dBov)
		stream.total++
	}
	a.Unlock()
}

func (a *AudioObserver) Calc() []string {
	var previous []string
	if n := len(a.previous); n > 0 {
		previous = make([]string, n)
		copy(previous, a.previous)
	}
	streamIDs := a.previous[:0]

	a.Lock()
	for _, s := range a.streams {
		if s.total >= a.expected {
			streamIDs = append(streamIDs, s.id)
		} else {
			s.total = 0
			s.sum = 0
		}
	}
	sort.Slice(streamIDs, func(i, j int) bool {
		si, sj := a.streams[streamIDs[i]], a.streams[streamIDs[j]]
		if si.total != sj.total {
			return si.total > sj.total
		}
		return si.sum < sj.sum
	})
	for _, id := range streamIDs {
		s := a.streams[id]
		s.total = 0
		s.sum = 0
	}
	a.Unlock()

	a.previous = streamIDs
	if len(previous) == len(streamIDs) {
		for i, s := range previous {
			if s != streamIDs[i] {
				return streamIDs
			}
		}
		return nil
	}

	return streamIDs
}
