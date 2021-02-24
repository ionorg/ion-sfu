package logger

import (
	"bytes"
	"log"
	"os"
	"testing"

	"github.com/bmizerany/assert"
)

func captureOutput(f func()) string {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	f()
	log.SetOutput(os.Stderr)
	return buf.String()
}

func TestLogLevelDefault(t *testing.T) {

	logger := New() // logger.verbose - 1

	output := captureOutput(func() {
		logger.Info("This is info log") // Writing log with V - 1
	})
	assert.Equal(t, "This is info log", output)

	output = captureOutput(func() {
		logger.V(2).Info("This is debug log")
	})
	assert.Equal(t, "", output) // Must be empty
}

// func TestLogLevelDebug(t *testing.T) {

// log := NewWithOptions(Options{Level: "debug"}) // or zerologr.NewWithOptions(logr.Options{VLevel: 2})

// output := captureOutput(func() {
// 	log.Info("This is info log")
// })
// assert.Equal(t, "This is info log", output)

// output = captureOutput(func() {
// 	log.V(2).Info("This is debug log")
// })
// assert.Equal(t, "This is info log", output)
// }
