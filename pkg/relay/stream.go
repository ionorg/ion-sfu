package relay

type readStream interface {
	init(child streamSession, sessionID uint32, ssrc uint32) error

	Read(buf []byte) (int, error)
	GetSSRC() uint32
	GetSessionID() uint32
}
