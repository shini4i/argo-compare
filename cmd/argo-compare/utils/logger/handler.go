package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
)

type handler struct {
	mu    sync.Mutex
	w     io.Writer
	level atomic.Int64
}

func newHandler(w io.Writer, level slog.Level) *handler {
	h := &handler{w: w}
	h.level.Store(int64(level))
	return h
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	if r.Level < slog.Level(h.level.Load()) {
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

func (h *handler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

func (h *handler) WithGroup(_ string) slog.Handler {
	return h
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.Level(h.level.Load())
}

var (
	currentHandler *handler
	mu             sync.Mutex
)

func init() {
	currentHandler = newHandler(os.Stdout, slog.LevelInfo)
}

func setLevel(level slog.Level) {
	mu.Lock()
	defer mu.Unlock()
	currentHandler.level.Store(int64(level))
}

func setOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	currentHandler.mu.Lock()
	currentHandler.w = w
	currentHandler.mu.Unlock()
}

func getHandlerInternal() slog.Handler {
	mu.Lock()
	defer mu.Unlock()
	return currentHandler
}
