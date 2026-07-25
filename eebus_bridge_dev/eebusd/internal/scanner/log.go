package scanner

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Package-level logger for the scanner package. It is configured by SetLogLevel
// (verbosity) and SetLogWriter (destination) from the app's main.
//
// In -json mode main() points logOut at stderr so stdout stays reserved for the
// NDJSON data stream consumed by eebus-bridge. Default is stdout at info level,
// so the scanner remains usable standalone before the app wires anything.

type logLevel int

const (
	levelTrace logLevel = iota
	levelDebug
	levelInfo
	levelWarn
	levelError
)

var (
	logMu     sync.Mutex
	logLevel_           = levelInfo
	logOut    io.Writer = os.Stdout
)

// SetLogLevel sets the scanner package log verbosity.
// Accepted values: "trace", "debug", "info", "warn", "error".
func SetLogLevel(s string) {
	logMu.Lock()
	defer logMu.Unlock()
	switch s {
	case "trace":
		logLevel_ = levelTrace
	case "debug":
		logLevel_ = levelDebug
	case "info":
		logLevel_ = levelInfo
	case "warn":
		logLevel_ = levelWarn
	case "error":
		logLevel_ = levelError
	}
}

// SetLogWriter overrides the destination. Pass os.Stderr when running in -json
// mode so logs do not pollute the NDJSON stream on stdout.
func SetLogWriter(w io.Writer) {
	logMu.Lock()
	logOut = w
	logMu.Unlock()
}

func emit(level logLevel, tag, msg string) {
	logMu.Lock()
	defer logMu.Unlock()
	if level < logLevel_ {
		return
	}
	w := logOut
	if w == nil {
		w = os.Stderr
	}
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	fmt.Fprintf(w, "%s %-5s %s\n", ts, tag, msg)
}

func logTracef(f string, a ...any) { emit(levelTrace, "TRACE", fmt.Sprintf(f, a...)) }
func logDebugf(f string, a ...any) { emit(levelDebug, "DEBUG", fmt.Sprintf(f, a...)) }
func logInfof(f string, a ...any)  { emit(levelInfo, "INFO", fmt.Sprintf(f, a...)) }
func logWarnf(f string, a ...any)  { emit(levelWarn, "WARN", fmt.Sprintf(f, a...)) }
func logErrorf(f string, a ...any) { emit(levelError, "ERROR", fmt.Sprintf(f, a...)) }
