package relay

type readStream interface {
	init(child streamSession, id uint32) error

	Read(buf []byte) (int, error)
	ID() uint32
}
