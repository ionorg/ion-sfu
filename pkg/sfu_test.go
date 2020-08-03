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
		Receiver: ReceiverConfig{
			Video: VideoReceiverConfig{
				REMBCycle:     2,
				PLICycle:      1,
				TCCCycle:      1,
				MaxBandwidth:  1000,
				MaxBufferTime: 1000,
			},
		},
	})

	session := s.NewSession("test session")
	assert.NotNil(t, session)
	assert.Len(t, s.sessions, 1)

	assert.Equal(t, session, s.GetSession("test session"))

	session.onCloseHandler()
	assert.Nil(t, s.GetSession("test session"))
	assert.Len(t, s.sessions, 0)
}
