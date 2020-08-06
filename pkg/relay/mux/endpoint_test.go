package mux

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEndpoint(t *testing.T) {
	endpoint := Endpoint{}
	assert.Nil(t, endpoint.SetDeadline(time.Time{}))
	assert.Nil(t, endpoint.SetReadDeadline(time.Time{}))
	assert.Nil(t, endpoint.SetWriteDeadline(time.Time{}))
}
