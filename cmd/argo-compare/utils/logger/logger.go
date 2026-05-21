// Package logger provides a thin wrapper around log/slog that preserves the
// message-only output format used by the project (no timestamps).
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
)

// Logger wraps *slog.Logger to expose the printf-style helpers
// (Debugf/Infof/Warningf/Errorf) used throughout the codebase.
type Logger struct {
	*slog.Logger
}

// New creates a new Logger. The name parameter is currently informational only;
// the underlying handler does not filter or label output by name.
func New(_ string) *Logger {
	return &Logger{slog.New(getHandlerInternal())}
}

// Debugf logs a formatted message at DEBUG level.
func (l *Logger) Debugf(format string, args ...interface{}) {
	l.Debug(fmt.Sprintf(format, args...))
}

// Infof logs a formatted message at INFO level.
func (l *Logger) Infof(format string, args ...interface{}) {
	l.Info(fmt.Sprintf(format, args...))
}

// Warningf logs a formatted message at WARN level.
func (l *Logger) Warningf(format string, args ...interface{}) {
	l.Warn(fmt.Sprintf(format, args...))
}

// Errorf logs a formatted message at ERROR level.
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.Error(fmt.Sprintf(format, args...))
}

// Warning logs a message at WARN level.
func (l *Logger) Warning(args ...interface{}) {
	l.Warn(fmt.Sprint(args...))
}

// Fatal logs a message at ERROR level and exits with code 1.
func (l *Logger) Fatal(args ...interface{}) {
	l.Error(fmt.Sprint(args...))
	os.Exit(1)
}

// SetLevel configures the global log level. When debugFlag is true, DEBUG-level
// messages are emitted; otherwise, only INFO and above.
func SetLevel(debugFlag bool) {
	if debugFlag {
		setLevel(slog.LevelDebug)
	} else {
		setLevel(slog.LevelInfo)
	}
}

// SetOutput redirects log output globally to w. All existing Logger instances
// pick up the change because they share the package-level handler.
func SetOutput(w io.Writer) {
	setOutput(w)
}

// RedirectForTest redirects log output to w for the duration of the test and
// registers a t.Cleanup to restore os.Stdout afterwards. This ensures no test
// leaks captured output state into subsequent tests.
func RedirectForTest(t *testing.T, w io.Writer) {
	t.Helper()
	SetOutput(w)
	t.Cleanup(func() { SetOutput(os.Stdout) })
}
