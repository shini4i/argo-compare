package logger

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogger_MessageOnlyFormat(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	t.Cleanup(func() { SetOutput(os.Stdout) })

	log := New("test-format")
	log.Info("hello world")

	assert.Equal(t, "hello world\n", buf.String(), "output must be message-only without timestamp or level prefix")
}

func TestLogger_PrintfHelpers(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	t.Cleanup(func() { SetOutput(os.Stdout) })

	log := New("test-printf")
	log.Infof("user=%s id=%d", "alice", 42)

	assert.Equal(t, "user=alice id=42\n", buf.String())
}

func TestLogger_DebugSuppressedByDefault(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(false)
	t.Cleanup(func() {
		SetOutput(os.Stdout)
		SetLevel(false)
	})

	log := New("test-debug-off")
	log.Debug("hidden debug")
	log.Info("visible info")

	require.NotContains(t, buf.String(), "hidden debug")
	require.Contains(t, buf.String(), "visible info")
}

func TestLogger_DebugEnabledAfterSetLevel(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(true)
	t.Cleanup(func() {
		SetOutput(os.Stdout)
		SetLevel(false)
	})

	log := New("test-debug-on")
	log.Debug("visible debug")

	require.Contains(t, buf.String(), "visible debug")
}

func TestLogger_SetOutputAffectsExistingLogger(t *testing.T) {
	log := New("test-shared-handler")

	var buf bytes.Buffer
	SetOutput(&buf)
	t.Cleanup(func() { SetOutput(os.Stdout) })

	log.Info("after-setoutput")

	assert.Contains(t, buf.String(), "after-setoutput", "SetOutput must affect loggers that were created before the call")
}

func TestLogger_RedirectForTest_RestoresOutput(t *testing.T) {
	var buf bytes.Buffer

	t.Run("captures during", func(t *testing.T) {
		RedirectForTest(t, &buf)
		log := New("redirect-test")
		log.Info("captured")
	})

	require.Contains(t, buf.String(), "captured")

	// After the subtest's cleanup runs, output must be restored to os.Stdout.
	// Verify by writing to a new buffer and confirming our original buf isn't affected.
	var buf2 bytes.Buffer
	RedirectForTest(t, &buf2)
	log := New("after-restore")
	log.Info("new-buffer")

	assert.NotContains(t, buf.String(), "new-buffer", "original buffer must not receive new writes after restore")
	assert.Contains(t, buf2.String(), "new-buffer")
}

func TestLogger_ConcurrentLogging(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	t.Cleanup(func() { SetOutput(os.Stdout) })

	const goroutines = 20
	const writesPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			log := New("concurrent")
			for j := 0; j < writesPerGoroutine; j++ {
				log.Info("line")
			}
		}()
	}
	wg.Wait()

	// Every line should be intact (no interleaved bytes). With message-only
	// format, every line is exactly "line\n".
	output := buf.String()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	require.Equal(t, goroutines*writesPerGoroutine, len(lines))
	for _, line := range lines {
		assert.Equal(t, "line", line)
	}
}

func TestLogger_DiscardOutput(t *testing.T) {
	SetOutput(io.Discard)
	t.Cleanup(func() { SetOutput(os.Stdout) })

	log := New("discard-test")
	// Must not panic.
	log.Info("nowhere")
}
