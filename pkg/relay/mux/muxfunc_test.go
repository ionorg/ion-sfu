package mux

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchAll(t *testing.T) {
	assert.True(t, MatchAll(nil))
}

func TestMatchRTPOrRTCP(t *testing.T) {
	bin := []byte{0x90}
	assert.True(t, MatchRTPOrRTCP(bin))

	bin = []byte{0x01}
	assert.False(t, MatchRTPOrRTCP(bin))

	bin = []byte{}
	assert.False(t, MatchRTPOrRTCP(bin))
}

func TestMatchRTP(t *testing.T) {
	bin := []byte{0x90}
	assert.True(t, MatchRTP(bin))

	bin = []byte{0x01, 0x02, 0x03, 0x04}
	assert.False(t, MatchRTP(bin))
}

func TestMatchRTCP(t *testing.T) {
	bin := []byte{0x90, 0xC1, 0xC1, 0xC1, 0xC1}
	assert.True(t, MatchRTCP(bin))

	bin = []byte{0x90, 0x90, 0x90, 0x90, 0x90}
	assert.False(t, MatchRTCP(bin))
}
