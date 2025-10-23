package app

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/op/go-logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternalDiffStrategyPresent(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "collector.sh")
	outputPath := filepath.Join(tmpDir, "out.txt")

	script := "#!/bin/sh\ncat >> \"$(dirname \"$0\")/out.txt\"\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))

	logger := logging.MustGetLogger("external-diff")
	logging.SetBackend(logging.NewLogBackend(io.Discard, "", 0))
	t.Cleanup(func() {
		logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))
	})

	strategy := ExternalDiffStrategy{
		Log:         logger,
		Tool:        scriptPath,
		ShowAdded:   true,
		ShowRemoved: true,
	}

	result := ComparisonResult{
		Added: []DiffOutput{
			{File: File{Name: "/added.yaml"}, Diff: "added diff"},
		},
		Removed: []DiffOutput{
			{File: File{Name: "/removed.yaml"}, Diff: "removed diff"},
		},
		Changed: []DiffOutput{
			{File: File{Name: "/changed.yaml"}, Diff: "changed diff"},
		},
	}

	require.NoError(t, strategy.Present(result))

	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	output := string(content)

	assert.Contains(t, output, "added diff")
	assert.Contains(t, output, "removed diff")
	assert.Contains(t, output, "changed diff")
}
