package logger

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"testing"
)

func captureOutput(f func()) string {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	f()
	log.SetOutput(os.Stderr)
	return buf.String()
}

func TestLogLevelDefault(t *testing.T) {

	// logger := New() // logger.verbose - 1
	logger := NewWithOptions(Options{Level: "info"})
	logger.Info("info log1")
	logger.V(0).Info("info log11")
	logger.V(1).Info("debug log1")
	logger.V(2).Info("trace log1")
	logger.Error(nil, "err1")
	logger.V(1).Error(nil, "err1")

	fmt.Println()
	logger = NewWithOptions(Options{Level: "debug"})
	logger.Info("info log2")
	logger.V(0).Info("info log22")
	logger.V(1).Info("debug log2")
	logger.V(2).Info("trace log2")
	logger.Error(nil, "err2")
	logger.V(1).Error(nil, "err22")

	fmt.Println()
	logger = NewWithOptions(Options{Level: "trace"})
	logger.Info("info log3")
	logger.V(0).Info("info log33")
	logger.V(1).Info("debug log3")
	logger.V(2).Info("trace log3")
	logger.Error(nil, "err3")
	logger.V(1).Error(nil, "err33")
	// output := captureOutput(func() {
	// 	logger.Info("This is info log") // Writing log with V - 1
	// })
	// assert.Equal(t, "This is info log", output)

	// output = captureOutput(func() {
	// 	logger.V(2).Info("This is debug log")
	// })
	// assert.Equal(t, "", output) // Must be empty
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
