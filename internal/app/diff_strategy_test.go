package app

import (
	"context"
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

	require.NoError(t, strategy.Present(context.Background(), result))

	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	output := string(content)

	assert.Contains(t, output, "added diff")
	assert.Contains(t, output, "removed diff")
	assert.Contains(t, output, "changed diff")
}

func TestExternalDiffStrategyRunToolValidation(t *testing.T) {
	logger := logging.MustGetLogger("test")
	logging.SetBackend(logging.NewLogBackend(io.Discard, "", 0))
	t.Cleanup(func() {
		logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))
	})

	tests := []struct {
		name                    string
		tool                    string
		expectValidationError   bool
		validationErrorContains string
	}{
		{
			name:                    "semicolon injection",
			tool:                    "diff; rm -rf /",
			expectValidationError:   true,
			validationErrorContains: "invalid diff tool name",
		},
		{
			name:                    "ampersand injection",
			tool:                    "diff & malicious",
			expectValidationError:   true,
			validationErrorContains: "invalid diff tool name",
		},
		{
			name:                    "pipe injection",
			tool:                    "diff | cat /etc/passwd",
			expectValidationError:   true,
			validationErrorContains: "invalid diff tool name",
		},
		{
			name:                    "backtick injection",
			tool:                    "diff`whoami`",
			expectValidationError:   true,
			validationErrorContains: "invalid diff tool name",
		},
		{
			name:                    "dollar sign injection",
			tool:                    "diff$(whoami)",
			expectValidationError:   true,
			validationErrorContains: "invalid diff tool name",
		},
		{
			name:                    "path traversal",
			tool:                    "../../../bin/sh",
			expectValidationError:   true,
			validationErrorContains: "invalid diff tool name",
		},
		{
			name:                    "quote injection",
			tool:                    "diff\"test",
			expectValidationError:   true,
			validationErrorContains: "invalid diff tool name",
		},
		{
			name:                  "valid simple tool name",
			tool:                  "diff",
			expectValidationError: false,
		},
		{
			name:                  "valid tool with hyphen",
			tool:                  "colordiff",
			expectValidationError: false,
		},
		{
			name:                  "valid absolute path",
			tool:                  "/usr/bin/diff",
			expectValidationError: false,
		},
		{
			name:                    "whitespace injection",
			tool:                    "diff --help",
			expectValidationError:   true,
			validationErrorContains: "invalid diff tool name",
		},
		{
			name:                    "newline injection",
			tool:                    "diff\necho",
			expectValidationError:   true,
			validationErrorContains: "invalid diff tool name",
		},
		{
			name:                    "tab injection",
			tool:                    "diff\techo",
			expectValidationError:   true,
			validationErrorContains: "invalid diff tool name",
		},
		{
			name:                    "empty tool name",
			tool:                    "",
			expectValidationError:   true,
			validationErrorContains: "invalid diff tool name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := ExternalDiffStrategy{
				Log:  logger,
				Tool: tt.tool,
			}

			err := strategy.runTool(context.Background(), "test diff content")

			if tt.expectValidationError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.validationErrorContains)
			} else {
				// Valid tool name should not produce a validation error
				// It may fail because the tool doesn't exist, but the error
				// should NOT be about invalid tool name
				if err != nil {
					assert.NotContains(t, err.Error(), "invalid diff tool name",
						"valid tool name should not trigger validation error")
				}
			}
		})
	}
}

func TestExternalDiffStrategyRunSectionCollectsErrors(t *testing.T) {
	logger := logging.MustGetLogger("test")
	logging.SetBackend(logging.NewLogBackend(io.Discard, "", 0))
	t.Cleanup(func() {
		logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))
	})

	strategy := ExternalDiffStrategy{
		Log:  logger,
		Tool: "nonexistent-tool-12345",
	}

	entries := []DiffOutput{
		{File: File{Name: "file1.yaml"}, Diff: "diff1"},
		{File: File{Name: "file2.yaml"}, Diff: "diff2"},
	}

	err := strategy.runSection(context.Background(), entries)

	require.Error(t, err)
	// Should contain errors for both files
	assert.Contains(t, err.Error(), "file1.yaml")
	assert.Contains(t, err.Error(), "file2.yaml")
}

func TestExternalDiffStrategyPresentEmpty(t *testing.T) {
	logger := logging.MustGetLogger("test")
	logging.SetBackend(logging.NewLogBackend(io.Discard, "", 0))
	t.Cleanup(func() {
		logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))
	})

	strategy := ExternalDiffStrategy{
		Log:         logger,
		Tool:        "diff",
		ShowAdded:   true,
		ShowRemoved: true,
	}

	err := strategy.Present(context.Background(), ComparisonResult{})

	require.NoError(t, err)
}

func TestExternalDiffStrategyPresentWithInvalidTool(t *testing.T) {
	logger := logging.MustGetLogger("test")
	logging.SetBackend(logging.NewLogBackend(io.Discard, "", 0))
	t.Cleanup(func() {
		logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))
	})

	strategy := ExternalDiffStrategy{
		Log:         logger,
		Tool:        "diff;rm",
		ShowAdded:   true,
		ShowRemoved: true,
	}

	result := ComparisonResult{
		Added: []DiffOutput{{File: File{Name: "test.yaml"}, Diff: "diff content"}},
	}

	err := strategy.Present(context.Background(), result)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid diff tool name")
}

func TestExternalDiffStrategyPresentShowFlags(t *testing.T) {
	logger := logging.MustGetLogger("test")
	logging.SetBackend(logging.NewLogBackend(io.Discard, "", 0))
	t.Cleanup(func() {
		logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))
	})

	// Test with ShowAdded=false, ShowRemoved=false - only Changed runs
	strategy := ExternalDiffStrategy{
		Log:         logger,
		Tool:        "nonexistent-tool",
		ShowAdded:   false,
		ShowRemoved: false,
	}

	result := ComparisonResult{
		Added:   []DiffOutput{{File: File{Name: "added.yaml"}, Diff: "added diff"}},
		Removed: []DiffOutput{{File: File{Name: "removed.yaml"}, Diff: "removed diff"}},
		Changed: []DiffOutput{{File: File{Name: "changed.yaml"}, Diff: "changed diff"}},
	}

	err := strategy.Present(context.Background(), result)

	require.Error(t, err)
	// Only Changed section should be processed
	assert.Contains(t, err.Error(), "changed.yaml")
	assert.NotContains(t, err.Error(), "added.yaml")
	assert.NotContains(t, err.Error(), "removed.yaml")
}

func TestValidateToolName(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		wantErr bool
	}{
		{"empty", "", true},
		{"path traversal", "../bin/sh", true},
		{"triple dots", "...", true},
		{"embedded path traversal", "/usr/../bin/sh", true},
		{"valid absolute", "/usr/bin/diff", false},
		{"valid simple", "diff", false},
		{"valid with underscore", "my_diff", false},
		{"valid with hyphen", "color-diff", false},
		{"valid with dot", "diff.sh", false},
		{"root slash only", "/", false},
		{"trailing slash", "diff/", false},
		{"semicolon", "diff;rm", true},
		{"ampersand", "diff&cmd", true},
		{"pipe", "diff|cat", true},
		{"backtick", "diff`whoami`", true},
		{"dollar sign", "diff$(cmd)", true},
		{"space", "diff --version", true},
		{"newline", "diff\necho", true},
		{"tab", "diff\techo", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateToolName(tt.tool)
			if tt.wantErr {
				assert.Error(t, err, "validateToolName(%q) should return error", tt.tool)
			} else {
				assert.NoError(t, err, "validateToolName(%q) should not return error", tt.tool)
			}
		})
	}
}
