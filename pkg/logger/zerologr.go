// Copyright 2019 Jorn Friedrich Dreyer
// Modified 2021 Serhii Mikhno
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
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
package logger

import (
	"github.com/go-logr/logr"
	"github.com/rs/zerolog"
)

type VLevel int

const (
	infoVLevel  VLevel = iota // info
	debugVLevel               // debug
	traceVLevel               // trace
	timeFormat  = "2006-01-02 15:04:05.000"
)

func SetLogLevelString(level string) {
	l := zerolog.GlobalLevel()
	switch level {
	case "trace":
		l = zerolog.TraceLevel
	case "debug":
		l = zerolog.DebugLevel
	case "info":
		l = zerolog.InfoLevel
	case "error":
		l = zerolog.ErrorLevel
	}
	zerolog.SetGlobalLevel(l)

}

func SetLogLevel(level int) {
	l := zerolog.GlobalLevel()
	switch level {
	case 0:
		l = zerolog.InfoLevel
	case 1:
		l = zerolog.DebugLevel
	case 3:
		l = zerolog.TraceLevel
	default:
		l = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(l)
}

// Options that can be passed to NewWithOptions
type Options struct {
	// Name is an optional name of the logger
	Name       string
	Level      string
	Vlevel     VLevel
	TimeFormat string

	// Logger is an instance of zerolog, if nil a default logger is used
	Logger *zerolog.Logger
}

// logger is a logr.Logger that uses zerolog to log.
type logger struct {
	l            *zerolog.Logger
	vlevel       VLevel
	parentVLevel VLevel
	prefix       string
	values       []interface{}
}

// New returns a logr.Logger which is implemented by zerolog.
func New() logr.Logger {
	return NewWithOptions(Options{})
}

// NewWithOptions returns a logr.Logger which is implemented by zerolog.
func NewWithOptions(opts Options) logr.Logger {

	var level VLevel
	if opts.TimeFormat == "" {
		opts.TimeFormat = timeFormat
	}

	if opts.Level != "" {
		level = getVLevelByString(opts.Level)
	} else {
		level = opts.Vlevel
	}

	if opts.Logger == nil {
		zerolog.TimeFieldFormat = timeFormat
		logLevel := getZerologLevelByVLevel(level)
		output := getOutputFormat()
		l := zerolog.New(output).Level(logLevel).With().Timestamp().Logger()
		// l := zerolog.New(output).With().Timestamp().Logger()
		opts.Logger = &l
	}

	return logger{
		l:            opts.Logger,
		vlevel:       level,
		parentVLevel: level,
		prefix:       opts.Name,
		values:       nil,
	}
}

func (l logger) Info(msg string, keysAndVals ...interface{}) {
	if l.Enabled() {
		var e *zerolog.Event
		if l.vlevel < debugVLevel {
			e = l.l.Info()
		} else if l.vlevel < traceVLevel {
			e = l.l.Debug()
		} else {
			e = l.l.Trace()
		}
		if l.prefix != "" {
			e.Str("name", l.prefix)
		}
		add(e, l.values)
		add(e, keysAndVals)
		e.Msg(msg)
	}
}

// Enabled Check that V-level for this Logger is bigger than parrent logger
func (l logger) Enabled() bool {
	return l.vlevel <= l.parentVLevel
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
	new.vlevel = VLevel(vlevel)
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

// Verify that logger implements logr.Logger.
var _ logr.Logger = logger{}

// var _ logr.CallDepthLogger = logger{}
