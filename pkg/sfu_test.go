package sfu

import (
	"testing"

	"github.com/pion/ion-sfu/pkg/log"
	"github.com/stretchr/testify/assert"
)

func TestSFU(t *testing.T) {
	s := NewSFU(Config{
		Log: log.Config{
			Level: "debug",
		},
		WebRTC: WebRTCConfig{
			ICEPortRange: []uint16{5000, 5200},
			ICEServers: []ICEServerConfig{{
				URLs:       []string{"test"},
				Username:   "me",
				Credential: "secret",
			}},
		},
	})

	room := s.CreateRoom("test room")
	assert.NotNil(t, room)
	assert.Len(t, s.rooms, 1)

	assert.Equal(t, room, s.GetRoom("test room"))

	room.onCloseHandler()
	assert.Nil(t, s.GetRoom("test room"))
	assert.Len(t, s.rooms, 0)
}
