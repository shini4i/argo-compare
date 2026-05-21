package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
)

type handler struct {
	mu     sync.Mutex
	w      io.Writer
	level  slog.Level
}

func newHandler(w io.Writer, level slog.Level) *handler {
	return &handler{w: w, level: level}
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < h.level {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	msg := r.Message
	if r.NumAttrs() > 0 {
		attrs := make([]string, 0, r.NumAttrs())
		r.Attrs(func(a slog.Attr) bool {
			attrs = append(attrs, fmt.Sprintf("%s=%v", a.Key, a.Value.Any()))
			return true
		})
		if len(attrs) > 0 {
			msg = fmt.Sprintf("%s %v", msg, attrs)
		}
	}

	fmt.Fprintf(h.w, "%s\n", msg)
	return nil
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *handler) WithGroup(name string) slog.Handler {
	return h
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level
}

var (
	currentHandler *handler
	currentLevel   = slog.LevelInfo
	mu             sync.Mutex
)

func init() {
	currentHandler = newHandler(os.Stdout, currentLevel)
}

func setHandler(h *handler) {
	mu.Lock()
	defer mu.Unlock()
	currentHandler = h
}

func getHandler() *handler {
	mu.Lock()
	defer mu.Unlock()
	return currentHandler
}

func setLevel(level slog.Level) {
	mu.Lock()
	defer mu.Unlock()
	currentLevel = level
	if currentHandler != nil {
		currentHandler.level = level
	}
}

func setOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	if currentHandler != nil {
		currentHandler.mu.Lock()
		currentHandler.w = w
		currentHandler.mu.Unlock()
	} else {
		currentHandler = newHandler(w, currentLevel)
	}
}

func getHandlerInternal() slog.Handler {
	mu.Lock()
	defer mu.Unlock()
	return currentHandler
}
