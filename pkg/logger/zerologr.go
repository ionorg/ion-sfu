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

const (
	zeroVLevel    = iota // error and warn
	defaultVLevel        // info
	debugVLevel          // debug
	traceVLevel          // trace
	timeFormat    = "2006-01-02 15:04:05.000"
)

// Options that can be passed to NewWithOptions
type Options struct {
	// Name is an optional name of the logger
	Name  string
	Level string
	// Logger is an instance of zerolog, if nil a default logger is used
	Logger *zerolog.Logger
}

// logger is a logr.Logger that uses zerolog to log.
type logger struct {
	l         *zerolog.Logger
	verbosity int
	prefix    string
	values    []interface{}
}

// New returns a logr.Logger which is implemented by zerolog.
func New() logr.Logger {
	return NewWithOptions(Options{})
}

// NewWithOptions returns a logr.Logger which is implemented by zerolog.
func NewWithOptions(opts Options) logr.Logger {

	// var logLevel
	// if opts.Level == "" {
	// 	logLevel = getZerologLevel(opts.Level)
	// }
	if opts.Logger == nil {
		zerolog.TimeFieldFormat = timeFormat
		logLevel := getZerologLevel(opts.Level)
		output := getOutputFormat()
		l := zerolog.New(output).Level(logLevel).With().Timestamp().Logger()
		opts.Logger = &l
	}

	return logger{
		l:         opts.Logger,
		verbosity: defaultVLevel,
		prefix:    opts.Name,
		values:    nil,
	}
}

func (l logger) Info(msg string, keysAndVals ...interface{}) {
	if l.Enabled() {
		var e *zerolog.Event
		if l.verbosity < debugVLevel {
			e = l.l.Info()
		} else if l.verbosity < traceVLevel {
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

// Enabled Check
func (l logger) Enabled() bool {
	var lvl zerolog.Level
	if l.verbosity == zeroVLevel {
		lvl = zerolog.WarnLevel
	} else if l.verbosity <= defaultVLevel {
		lvl = zerolog.InfoLevel
	} else if l.verbosity <= debugVLevel {
		lvl = zerolog.DebugLevel
	} else if l.verbosity <= traceVLevel {
		lvl = zerolog.TraceLevel
	} else {
		lvl = zerolog.DebugLevel
	}
	if lvl < zerolog.GlobalLevel() {
		return false
	}
	return true
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
func (l logger) V(verbosity int) logr.Logger {
	new := l.clone()
	new.verbosity = verbosity
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
