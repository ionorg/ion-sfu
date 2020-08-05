package sfu

// func TestSFU(t *testing.T) {
// 	s := NewSFU(Config{
// 		Log: log.Config{
// 			Level: "error",
// 		},
// 		WebRTC: WebRTCConfig{},
// 		Receiver: ReceiverConfig{
// 			Video: WebRTCVideoReceiverConfig{},
// 		},
// 	})

// 	session := s.NewSession("test session")
// 	assert.NotNil(t, session)
// 	assert.Len(t, s.sessions, 1)

// 	assert.Equal(t, session, s.GetSession("test session"))

// 	session.onCloseHandler()
// 	assert.Nil(t, s.GetSession("test session"))
// 	assert.Len(t, s.sessions, 0)
// }
