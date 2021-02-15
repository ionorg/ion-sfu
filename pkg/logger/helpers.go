package logger

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rs/zerolog"
)

func getZerologLevel(level string) zerolog.Level {
	zerologlvl := zerolog.GlobalLevel()
	switch level {
	case "trace":
		zerologlvl = traceVLevel
	case "debug":
		zerologlvl = debugVLevel
	case "info":
		zerologlvl = defaultVLevel
	case "warn":
		zerologlvl = 0
	case "error":
		zerologlvl = 0
	}
	return zerologlvl
}

func getCallerFuncAndFile() (string, string, int) {
	// Took this function from https://github.com/projectcalico/libcalico-go/blob/3d85c5968bd031dd6f0bfbefe753f22d1f0ef250/lib/logutils/logutils.go#L162
	pcs := make([]uintptr, 10)
	if numEntries := runtime.Callers(1, pcs); numEntries > 0 {
		pcs = pcs[:numEntries]
		frames := runtime.CallersFrames(pcs)
		for {
			frame, more := frames.Next()
			if !shouldSkipFrame(frame) {
				// We found the frame we were looking for.  Record its file/line number.
				return path.Base(frame.File), strings.TrimPrefix(frame.Function, "."), frame.Line
			}
			if !more {
				return "", "", 0
			}
		}
	}
	return "", "", 0
}

func shouldSkipFrame(frame runtime.Frame) bool {
	if strings.HasSuffix(frame.File, "/helpers.go") ||
		strings.HasSuffix(frame.File, "/console.go") ||
		strings.HasSuffix(frame.File, "/zerologr.go") {
		if strings.Contains(frame.File, "/zerolog") {
			return true
		}
	}
	if strings.HasSuffix(frame.File, "/logger/helpers.go") {
		if strings.Contains(frame.File, "pion/ion-sfu") {
			return true
		}
	}
	return false
}

// func getOutputFormat(level string, fixByFile, fixByFunc []string) zerolog.ConsoleWriter {
func getOutputFormat() zerolog.ConsoleWriter {
	output := zerolog.ConsoleWriter{Out: os.Stdout, NoColor: false, TimeFormat: timeFormat}
	output.FormatTimestamp = func(i interface{}) string {
		return "[" + i.(string) + "]"
	}
	output.FormatLevel = func(i interface{}) string {
		return strings.ToUpper(fmt.Sprintf("[%-3s]", i))
	}
	output.FormatMessage = func(i interface{}) string {
		_, file, line, _ := runtime.Caller(8)
		return fmt.Sprintf("[%s:%d] => %s", filepath.Base(file), line, i)
	}
	return output
}

func (l logger) clone() logger {
	out := l
	out.values = copySlice(l.values)
	return out
}

func copySlice(in []interface{}) []interface{} {
	out := make([]interface{}, len(in))
	copy(out, in)
	return out
}

// add converts a bunch of arbitrary key-value pairs into zerolog fields.
func add(e *zerolog.Event, keysAndVals []interface{}) {

	// make sure we got an even number of arguments
	if len(keysAndVals)%2 != 0 {
		e.Interface("args", keysAndVals).
			AnErr("zerologr-err", errors.New("odd number of arguments passed as key-value pairs for logging")).
			Stack()
		return
	}

	for i := 0; i < len(keysAndVals); {
		// process a key-value pair,
		// ensuring that the key is a string
		key, val := keysAndVals[i], keysAndVals[i+1]
		keyStr, isString := key.(string)
		if !isString {
			// if the key isn't a string, log additional error
			e.Interface("invalid key", key).
				AnErr("zerologr-err", errors.New("non-string key argument passed to logging, ignoring all later arguments")).
				Stack()
			return
		}
		e.Interface(keyStr, val)

		i += 2
	}
}
