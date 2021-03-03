// Copyright 2019 Jorn Friedrich Dreyer
// Modified 2021 Serhii Mikhno
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance  the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package logger defines a default implementation of the github.com/go-logr/logr
// interfaces built on top of zerolog (github.com/rs/zerolog) and is the default
// implementation for ion-sfu released binaries.

// This package separates log level into two different concepts:
// - V-Level - verbosity level, number, that every logger has.
//   A higher value that means more logs will be written.
// - Log-level - usual log level (TRACE|DEBUG|INFO).
// Every log row combines those two values.
// You can set log level to TRACE and see all general traces.
// To see more logs just add -v

package logger

import (
	"io"
	"path/filepath"
	"runtime"
	"sync/atomic"

	"github.com/go-logr/logr"
	"github.com/rs/zerolog"
)

const (
	infoVLevel  = iota // info
	debugVLevel        // debug
	traceVLevel        // trace
	timeFormat  = "2006-01-02 15:04:05.000"
)

//GlobalConfig config contains global options
type GlobalConfig struct {
	V int `mapstructure:"v"`
}

var (
	globalVLevel = new(int32)
)

// SetGlobalOptions sets the global options, like level against which all info logs will be
// compared.  If this is greater than or equal to the "V" of the logger, the
// message will be logged. Concurrent-safe.
func SetGlobalOptions(config GlobalConfig) {
	atomic.StoreInt32(globalVLevel, int32(config.V))
}

// SetVLevelByStringGlobal does the same as SetGlobalOptions but
// trying to expose verbosity level as more familiar "word-based" log levels
func SetVLevelByStringGlobal(level string) {
	switch level {
	case "trace":
		SetGlobalOptions(GlobalConfig{V: traceVLevel})
	case "debug":
		SetGlobalOptions(GlobalConfig{V: debugVLevel})
	case "info":
		SetGlobalOptions(GlobalConfig{V: infoVLevel})
	default:
		SetGlobalOptions(GlobalConfig{V: infoVLevel})
	}

}

// Options that can be passed to NewWithOptions
type Options struct {
	// Name is an optional name of the logger
	Name       string
	TimeFormat string
	Output     io.Writer
	// Logger is an instance of zerolog, if nil a default logger is used
	Logger *zerolog.Logger
}

type logger struct {
	l      *zerolog.Logger
	vlevel int
	prefix string
	depth  int
	values []interface{}
}

type callerID struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

func (l logger) caller() callerID {
	_, file, line, ok := runtime.Caller(framesToCaller() + l.depth + 1) // +1 for this frame
	if !ok {
		return callerID{"<unknown>", 0}
	}
	return callerID{filepath.Base(file), line}
}

// New returns a logr.Logger which is implemented by zerolog.
func New() logr.Logger {
	return NewWithOptions(Options{})
}

// NewWithOptions returns a logr.Logger which is implemented by zerolog.
func NewWithOptions(opts Options) logr.Logger {

	zerolog.TimeFieldFormat = timeFormat
	if opts.TimeFormat != "" {
		zerolog.TimeFieldFormat = opts.TimeFormat
	}

	var out io.Writer
	out = getOutputFormat()
	if opts.Output != nil {
		out = opts.Output
	}

	if opts.Logger == nil {
		l := zerolog.New(out).With().Timestamp().Logger()
		opts.Logger = &l
	}

	return logger{
		l:      opts.Logger,
		vlevel: 0,
		prefix: opts.Name,
		values: nil,
	}
}

func (l logger) Info(msg string, keysAndVals ...interface{}) {
	// Checking that logger vlevel isn't greater than global verbosity level
	// and
	if l.Enabled() {
		var e *zerolog.Event
		if l.vlevel == infoVLevel {
			e = l.l.Info()
		} else if l.vlevel == debugVLevel {
			e = l.l.Debug()
		} else if l.vlevel >= traceVLevel {
			e = l.l.Trace()
		}
		if l.prefix != "" {
			e.Str("name", l.prefix)
		}
		keysAndVals = append(keysAndVals, "v", l.vlevel)
		add(e, l.values)
		add(e, keysAndVals)
		e.Msg(msg)
	}
}

// Enabled checks that the global V-Level is not less than logger V-Level
func (l logger) Enabled() bool {
	return l.vlevel <= int(atomic.LoadInt32(globalVLevel))
}

// Error always prints error, not metter which log level was set
func (l logger) Error(err error, msg string, keysAndVals ...interface{}) {
	e := l.l.Error().Err(err)
	if l.prefix != "" {
		e.Str("name", l.prefix)
	}
	add(e, l.values)
	add(e, keysAndVals)
	e.Msg(msg)
}

// V returns new logger with less or more log level
func (l logger) V(vlevel int) logr.Logger {
	new := l.clone()
	new.vlevel += vlevel
	return new
}

// WithName returns a new logr.Logger with the specified name appended. zerologr
// uses '/' characters to separate name elements.  Callers should not pass '/'
// in the provided name string, but this library does not actually enforce that.
func (l logger) WithName(name string) logr.Logger {
	new := l.clone()
	if len(l.prefix) > 0 {
		new.prefix = l.prefix + "/"
	}
	new.prefix += name
	return new
}
func (l logger) WithValues(kvList ...interface{}) logr.Logger {
	new := l.clone()
	new.values = append(new.values, kvList...)
	return new
}

func (l logger) WithCallDepth(depth int) logr.Logger {
	new := l.clone()
	new.depth += depth
	return new
}
