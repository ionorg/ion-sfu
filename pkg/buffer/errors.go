package buffer

import "errors"

var (
	errPacketNotFound = errors.New("packet not found in cache")
	errPacketTooOld   = errors.New("packet not found in cache, too old")
	errExtNotFound    = errors.New("ext not found")
)
