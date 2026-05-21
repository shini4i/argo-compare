package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

type Logger struct {
	*slog.Logger
}

func New(name string) *Logger {
	h := getHandlerInternal()
	return &Logger{slog.New(h)}
}

func (l *Logger) Debugf(format string, args ...interface{}) {
	l.Debug(fmt.Sprintf(format, args...))
}

func (l *Logger) Infof(format string, args ...interface{}) {
	l.Info(fmt.Sprintf(format, args...))
}

func (l *Logger) Warningf(format string, args ...interface{}) {
	l.Warn(fmt.Sprintf(format, args...))
}

func (l *Logger) Errorf(format string, args ...interface{}) {
	l.Error(fmt.Sprintf(format, args...))
}

func (l *Logger) Fatal(args ...interface{}) {
	l.Error(fmt.Sprint(args...))
	os.Exit(1)
}

func (l *Logger) Warning(args ...interface{}) {
	l.Warn(fmt.Sprint(args...))
}

func SetLevel(debugFlag bool) {
	if debugFlag {
		setLevel(slog.LevelDebug)
	} else {
		setLevel(slog.LevelInfo)
	}
}

func SetOutput(w io.Writer) {
	setOutput(w)
}
