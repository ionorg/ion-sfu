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

// package zerologr defines an implementation of the github.com/go-logr/logr
// interfaces built on top of zerolog (github.com/rs/zerolog).

package logger

import (
	"fmt"
	"os"
	"sync"

	"github.com/go-logr/logr"
	"github.com/rs/zerolog"
)

const (
	debugVerbosity = 2
	traceVerbosity = 8
	timeFormat     = "2006-01-02 15:04:05.000"
)

var (
	log Zerologr
	mu  sync.RWMutex
)

// this prevents from using non-initialised logger
func init() {
	fixByFile := []string{"asm_amd64.s", "proc.go", "zerologr.go"}
	fixByFunc := []string{"Debugf", "Infof", "Warnf"}
	Init("debug", fixByFile, fixByFunc)
}

type Zerologr interface {
	logr.Logger
	Infof(string, ...interface{})
	Errorf(string, ...interface{})
	Panicf(string, ...interface{})
}

// Config seems like isn't somewhere used, so just leave it here to don't let old code fail
type Config struct {
	Level string `mapstructure:"level"`
}

// Start creates and starts logger that has the same output to pion/ion-log
func Init(level string, fixByFile, fixByFunc []string) {

	zerolog.TimeFieldFormat = timeFormat

	logLevel := getZerologLevel(level)
	output := getOutputFormat(level, fixByFile, fixByFunc)
	l := zerolog.New(output).Level(logLevel).With().Timestamp().Logger()

	o := Options{
		Name:   "",
		Logger: &l,
	}
	mu.Lock()
	log = NewWithOptions(o)
	mu.Unlock()
}

// New returns a logr.Logger which is implemented by zerolog.
func New() Zerologr {
	return NewWithOptions(Options{})
}

// NewWithOptions returns a logr.Logger which is implemented by zerolog.
func NewWithOptions(opts Options) Zerologr {
	if opts.Logger == nil {
		l := zerolog.New(os.Stdout).With().Timestamp().Logger()
		opts.Logger = &l
	}
	return logger{
		l:         opts.Logger,
		verbosity: int(opts.Logger.GetLevel()),
		prefix:    opts.Name,
		values:    nil,
	}
}

// Options that can be passed to NewWithOptions
type Options struct {
	// Name is an optional name of the logger
	Name string
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

func (l logger) Info(msg string, keysAndVals ...interface{}) {
	if l.Enabled() {
		var e *zerolog.Event
		if l.verbosity < debugVerbosity {
			e = l.l.Info()
		} else if l.verbosity < traceVerbosity {
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

func (l logger) Enabled() bool {
	var lvl zerolog.Level
	if l.verbosity < debugVerbosity {
		lvl = zerolog.InfoLevel
	} else if l.verbosity < traceVerbosity {
		lvl = zerolog.DebugLevel
	} else {
		lvl = zerolog.TraceLevel
	}
	if lvl < zerolog.GlobalLevel() {
		return false
	}
	return true
}

func (l logger) Error(err error, msg string, keysAndVals ...interface{}) {
	e := l.l.Error().Err(err)
	if l.prefix != "" {
		e.Str("name", l.prefix)
	}
	add(e, l.values)
	add(e, keysAndVals)
	e.Msg(msg)
}

func (l logger) V(verbosity int) logr.InfoLogger {
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

// Infof logs a formatted info level log to the console
func (l logger) Infof(format string, v ...interface{}) {
	l.Info(fmt.Sprintf(format, v...))
}

func Infof(format string, v ...interface{}) {
	mu.RLock()
	defer mu.RUnlock()
	log.Info(fmt.Sprintf(format, v...))
}

// Errorf logs a formatted error level log to the console
func (l logger) Errorf(format string, v ...interface{}) {
	l.Error(nil, fmt.Sprintf(format, v...))
}

func Errorf(format string, v ...interface{}) {
	mu.RLock()
	defer mu.RUnlock()
	log.Error(nil, fmt.Sprintf(format, v...))
}

// Panicf generates a panic event and stop all underlying goroutines
func (l logger) Panicf(format string, v ...interface{}) {
	msg := l.l.Panic()
	msg.Msgf(format, v)
}

func Panicf(format string, v ...interface{}) {
	mu.RLock()
	defer mu.RUnlock()
	log.Panicf(format, v)
}

// Debugf prints debug message with given format
func Debugf(format string, v ...interface{}) {
	mu.RLock()
	defer mu.RUnlock()
	log.Info(fmt.Sprintf(format, v...))
}

// Debugf prints warning message with given format
func Warnf(format string, v ...interface{}) {
	mu.RLock()
	defer mu.RUnlock()
	log.Info(fmt.Sprintf(format, v...))
}
