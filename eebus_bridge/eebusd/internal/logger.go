package internal

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// LogLevel represents the verbosity level for the application logger.
// It implements github.com/enbility/ship-go/logging.LoggingInterface
// so it can be wired into service.Service.SetLogging.
type LogLevel int

const (
	LogLevelTrace LogLevel = iota
	LogLevelDebug
	LogLevelInfo
	LogLevelWarn
	LogLevelError
)

// ParseLogLevel converts a textual level (trace/debug/info/error) into a LogLevel.
// Unknown values fall back to LogLevelInfo.
func ParseLogLevel(s string) LogLevel {
	switch s {
	case "trace":
		return LogLevelTrace
	case "debug":
		return LogLevelDebug
	case "info":
		return LogLevelInfo
	case "warn":
		return LogLevelWarn
	case "error":
		return LogLevelError
	default:
		return LogLevelInfo
	}
}

// Logger is a minimal level-aware logger that satisfies ship-go's
// logging.LoggingInterface (Trace/Tracef/Debug/Debugf/Info/Infof/Error/Errorf).
// It is concurrency-safe: SHIP/SPINE emit log lines from many goroutines.
//
// The destination io.Writer is configurable via SetWriter. In NDJSON (-json)
// mode the app points it at stderr so stdout stays reserved for the data
// stream — otherwise logs and JSON would interleave and break downstream
// consumers (e.g. the eebus-bridge subprocess pipe).
type Logger struct {
	mu     sync.Mutex
	level  LogLevel
	writer io.Writer
}

// NewLogger returns a Logger configured for the given level, writing to stdout
// by default (preserves the pre-NDJSON behavior).
func NewLogger(level LogLevel) *Logger {
	return &Logger{level: level, writer: os.Stdout}
}

// SetWriter overrides the destination. Pass os.Stderr when running in -json
// mode so logs do not pollute the NDJSON stream on stdout.
func (l *Logger) SetWriter(w io.Writer) {
	l.mu.Lock()
	l.writer = w
	l.mu.Unlock()
}

// SetLevel updates the level at runtime.
func (l *Logger) SetLevel(level LogLevel) {
	l.mu.Lock()
	l.level = level
	l.mu.Unlock()
}

func (l *Logger) ts() string {
	return time.Now().Format("2006-01-02 15:04:05.000")
}

func (l *Logger) emit(level LogLevel, tag, msg string) {
	if level < l.level {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.writer
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintf(w, "%s %-5s %s\n", l.ts(), tag, msg)
}

// Trace / Tracef
func (l *Logger) Trace(args ...interface{}) { l.emit(LogLevelTrace, "TRACE", fmt.Sprint(args...)) }
func (l *Logger) Tracef(f string, a ...any) { l.emit(LogLevelTrace, "TRACE", fmt.Sprintf(f, a...)) }

// Debug / Debugf
func (l *Logger) Debug(args ...interface{}) { l.emit(LogLevelDebug, "DEBUG", fmt.Sprint(args...)) }
func (l *Logger) Debugf(f string, a ...any) { l.emit(LogLevelDebug, "DEBUG", fmt.Sprintf(f, a...)) }

// Info / Infof
func (l *Logger) Info(args ...interface{}) { l.emit(LogLevelInfo, "INFO", fmt.Sprint(args...)) }
func (l *Logger) Infof(f string, a ...any) { l.emit(LogLevelInfo, "INFO", fmt.Sprintf(f, a...)) }

// Warn / Warnf
func (l *Logger) Warn(args ...interface{}) { l.emit(LogLevelWarn, "WARN", fmt.Sprint(args...)) }
func (l *Logger) Warnf(f string, a ...any) { l.emit(LogLevelWarn, "WARN", fmt.Sprintf(f, a...)) }

// Error / Errorf
func (l *Logger) Error(args ...interface{}) { l.emit(LogLevelError, "ERROR", fmt.Sprint(args...)) }
func (l *Logger) Errorf(f string, a ...any) { l.emit(LogLevelError, "ERROR", fmt.Sprintf(f, a...)) }

// AppLog is a package-level logger used by non-ship code (scanner, handlers).
// It is initialized in main() with the configured level. Using a global keeps
// the scanner code readable without threading the logger through every call.
var AppLog *Logger

// InitAppLog initializes the global AppLog. Must be called once at startup.
// The writer argument controls where logs go: pass os.Stderr in -json mode
// (so stdout is reserved for NDJSON) and os.Stdout otherwise.
func InitAppLog(level LogLevel, writer io.Writer) {
	if writer == nil {
		writer = os.Stdout
	}
	AppLog = &Logger{level: level, writer: writer}
}

// SetAppLogWriter redirects the global AppLog output. Used by main() once it
// knows whether -json is active.
func SetAppLogWriter(w io.Writer) {
	if AppLog != nil {
		AppLog.SetWriter(w)
	}
}
