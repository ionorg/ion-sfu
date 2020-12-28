package buffer

import "errors"

var (
	errPacketNotFound = errors.New("packet not found in cache")
	errBufferTooSmall = errors.New("buffer too small")
	errExtNotFound    = errors.New("ext not found")
)
