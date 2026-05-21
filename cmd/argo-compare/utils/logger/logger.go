// Package logger provides minimal message-only logging with a debug toggle.
//
// Output is written line-by-line to a configurable writer (default os.Stdout)
// with no timestamp, level, or name prefix. Debug-level calls are gated by a
// global toggle; all other levels are always emitted.
package logger

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

var (
	debugEnabled atomic.Bool

	mu  sync.Mutex
	out io.Writer = os.Stdout
)

// Logger is the type held by structs that need to log. All instances share the
// same global output and debug state; the name supplied to New is informational
// only.
type Logger struct{}

// New returns a Logger. The name is accepted for API compatibility and is not
// currently surfaced in output.
func New(_ string) *Logger {
	return &Logger{}
}

func writeLine(s string) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintln(out, s)
}

// Debug logs at DEBUG level when debug output is enabled.
func (l *Logger) Debug(args ...interface{}) {
	if debugEnabled.Load() {
		writeLine(fmt.Sprint(args...))
	}
}

// Debugf logs a formatted message at DEBUG level when debug output is enabled.
func (l *Logger) Debugf(format string, args ...interface{}) {
	if debugEnabled.Load() {
		writeLine(fmt.Sprintf(format, args...))
	}
}

// Info logs a message.
func (l *Logger) Info(args ...interface{}) {
	writeLine(fmt.Sprint(args...))
}

// Infof logs a formatted message.
func (l *Logger) Infof(format string, args ...interface{}) {
	writeLine(fmt.Sprintf(format, args...))
}

// Warning logs a message.
func (l *Logger) Warning(args ...interface{}) {
	writeLine(fmt.Sprint(args...))
}

// Warningf logs a formatted message.
func (l *Logger) Warningf(format string, args ...interface{}) {
	writeLine(fmt.Sprintf(format, args...))
}

// Error logs a message.
func (l *Logger) Error(args ...interface{}) {
	writeLine(fmt.Sprint(args...))
}

// Errorf logs a formatted message.
func (l *Logger) Errorf(format string, args ...interface{}) {
	writeLine(fmt.Sprintf(format, args...))
}

// Fatal logs a message and exits with code 1.
func (l *Logger) Fatal(args ...interface{}) {
	writeLine(fmt.Sprint(args...))
	os.Exit(1)
}

// SetLevel enables DEBUG-level output when debugFlag is true; otherwise DEBUG
// messages are suppressed.
func SetLevel(debugFlag bool) {
	debugEnabled.Store(debugFlag)
}

// SetOutput redirects log output globally to w.
func SetOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	out = w
}

// RedirectForTest redirects log output to w for the duration of the test and
// restores os.Stdout on cleanup. Ensures tests do not leak captured output into
// later tests.
func RedirectForTest(t *testing.T, w io.Writer) {
	t.Helper()
	SetOutput(w)
	t.Cleanup(func() { SetOutput(os.Stdout) })
}
