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

	"github.com/go-logr/logr"
	"github.com/rs/zerolog"
)

const (
	infoVLevel  = iota // info
	debugVLevel        // debug
	traceVLevel        // trace
	timeFormat  = "2006-01-02 15:04:05.000"
)

// GlobalConfig config contains global options
type GlobalConfig struct {
	V int `mapstructure:"v"`
}

// SetGlobalOptions sets the global options, like level against which all info logs will be
// compared.  If this is greater than or equal to the "V" of the logger, the
// message will be logged. Concurrent-safe.
func SetGlobalOptions(config GlobalConfig) {
	zerolog.SetGlobalLevel(toZerologLevel(config.V))
}

// SetVLevelByStringGlobal does the same as SetGlobalOptions but
// trying to expose verbosity level as more familiar "word-based" log levels
func SetVLevelByStringGlobal(level string) {
	v := infoVLevel
	switch level {
	case zerolog.TraceLevel.String():
		v = traceVLevel
	case zerolog.DebugLevel.String():
		v = debugVLevel
	}
	SetGlobalOptions(GlobalConfig{V: v})
}

// logr ensure no negative values.
func toZerologLevel(lvl int) zerolog.Level {
	if lvl > traceVLevel {
		lvl = traceVLevel
	}
	return zerolog.Level(1 - lvl)
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
	prefix string
	depth  int
	values []interface{}
}

// New returns a logr.Logger, LogSink is implemented by zerolog.
func New() logr.Logger {
	return NewWithOptions(Options{})
}

// NewWithOptions returns a logr.Logger, LogSink is implemented by zerolog.
func NewWithOptions(opts Options) logr.Logger {
	if opts.TimeFormat != "" {
		zerolog.TimeFieldFormat = opts.TimeFormat
	} else {
		zerolog.TimeFieldFormat = timeFormat
	}

	var out io.Writer
	if opts.Output != nil {
		out = opts.Output
	} else {
		out = getOutputFormat()
	}

	if opts.Logger == nil {
		l := zerolog.New(out).With().Timestamp().Logger()
		opts.Logger = &l
	}

	zl := &logger{
		l:      opts.Logger,
		prefix: opts.Name,
	}
	return logr.New(zl)
}

func (l *logger) Init(ri logr.RuntimeInfo) {
	l.depth = ri.CallDepth + zerolog.CallerSkipFrameCount + 1
}

func (l *logger) Info(lvl int, msg string, keysAndVals ...interface{}) {
	e := l.l.WithLevel(toZerologLevel(lvl))
	if e == nil {
		return
	}
	if l.prefix != "" {
		e.Str("name", l.prefix)
	}
	keysAndVals = append(keysAndVals, "v", lvl)
	add(e, l.values)
	add(e, keysAndVals)
	e.Msg(msg)
}

// Enabled checks that the global V-Level is not less than logger V-Level
func (l *logger) Enabled(lvl int) bool {
	return toZerologLevel(lvl) >= zerolog.GlobalLevel()
}

// Error always prints error, not metter which log level was set
func (l *logger) Error(err error, msg string, keysAndVals ...interface{}) {
	e := l.l.Error().Err(err)
	if l.prefix != "" {
		e.Str("name", l.prefix)
	}
	add(e, l.values)
	add(e, keysAndVals)
	e.Msg(msg)
}

// WithName returns a new logr.Logger with the specified name appended. zerologr
// uses '/' characters to separate name elements.  Callers should not pass '/'
// in the provided name string, but this library does not actually enforce that.
func (l logger) WithName(name string) logr.LogSink {
	if len(l.prefix) > 0 {
		l.prefix += "/"
	}
	l.prefix += name
	return &l

}

func (l logger) WithValues(kvList ...interface{}) logr.LogSink {
	// Three slice args forces a copy.
	n := len(l.values)
	l.values = append(l.values[:n:n], kvList...)
	return &l
}

func (l logger) WithCallDepth(depth int) logr.LogSink {
	l.depth += depth
	ll := l.l.With().CallerWithSkipFrameCount(l.depth).Logger()
	l.l = &ll
	return &l
}

var _ logr.LogSink = &logger{}
var _ logr.CallDepthLogSink = &logger{}
