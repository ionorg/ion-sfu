package logger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rs/zerolog"
)

func getZerologLevel(level string) zerolog.Level {
	zerologlvl := zerolog.GlobalLevel()
	switch level {
	case "trace":
		zerologlvl = zerolog.TraceLevel
	case "debug":
		zerologlvl = zerolog.DebugLevel
	case "info":
		zerologlvl = zerolog.InfoLevel
	case "warn":
		zerologlvl = zerolog.WarnLevel
	case "error":
		zerologlvl = zerolog.ErrorLevel
	}
	return zerologlvl
}

func getOutputFormat(level string, fixByFile, fixByFunc []string) zerolog.ConsoleWriter {
	output := zerolog.ConsoleWriter{Out: os.Stdout, NoColor: false, TimeFormat: timeFormat}
	output.FormatTimestamp = func(i interface{}) string {
		return "[" + i.(string) + "]"
	}
	output.FormatLevel = func(i interface{}) string {
		return strings.ToUpper(fmt.Sprintf("[%-3s]", i))
	}
	output.FormatMessage = func(i interface{}) string {
		caller, file, line, _ := runtime.Caller(9)
		fileName := filepath.Base(file)
		funcName := strings.TrimPrefix(filepath.Ext((runtime.FuncForPC(caller).Name())), ".")
		var needfix bool
		for _, b := range fixByFile {
			if strings.Contains(fileName, b) {
				needfix = true
			}
		}
		for _, b := range fixByFunc {
			if strings.Contains(funcName, b) {
				needfix = true
			}
		}
		if needfix {
			caller, file, line, _ = runtime.Caller(8)
			fileName = filepath.Base(file)
			funcName = strings.TrimPrefix(filepath.Ext((runtime.FuncForPC(caller).Name())), ".")
		}
		return fmt.Sprintf("[%d][%s][%s] => %s", line, fileName, funcName, i)
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
