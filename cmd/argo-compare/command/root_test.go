package command

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shini4i/argo-compare/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteRunsAppWithFlags(t *testing.T) {
	var receivedConfig app.Config

	opts := Options{
		Version:          "test-version",
		CacheDir:         t.TempDir(),
		TempDirBase:      os.TempDir(),
		ExternalDiffTool: "diff-tool",
		InitLogging:      func(bool) {},
		RunApp: func(cfg app.Config) error {
			receivedConfig = cfg
			return nil
		},
	}

	args := []string{
		"branch", "main",
		"--file", "app.yaml",
		"--ignore", "foo.yaml",
		"--preserve-helm-labels",
		"--print-added-manifests",
		"--print-removed-manifests",
	}

	err := Execute(opts, args)
	require.NoError(t, err)

	assert.Equal(t, "main", receivedConfig.TargetBranch)
	assert.Equal(t, "app.yaml", receivedConfig.FileToCompare)
	assert.Equal(t, []string{"foo.yaml"}, receivedConfig.FilesToIgnore)
	assert.True(t, receivedConfig.PreserveHelmLabels)
	assert.True(t, receivedConfig.PrintAddedManifests)
	assert.True(t, receivedConfig.PrintRemovedManifests)
	assert.Equal(t, "diff-tool", receivedConfig.ExternalDiffTool)
	assert.Equal(t, "test-version", receivedConfig.Version)
}

func TestExecuteHonoursFullOutputFlag(t *testing.T) {
	var receivedConfig app.Config

	opts := Options{
		Version:          "test-version",
		CacheDir:         t.TempDir(),
		TempDirBase:      os.TempDir(),
		ExternalDiffTool: "",
		InitLogging:      func(bool) {},
		RunApp: func(cfg app.Config) error {
			receivedConfig = cfg
			return nil
		},
	}

	err := Execute(opts, []string{"branch", "main", "--full-output"})
	require.NoError(t, err)

	assert.True(t, receivedConfig.PrintAddedManifests)
	assert.True(t, receivedConfig.PrintRemovedManifests)
}

func TestExecuteDropCache(t *testing.T) {
	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache")
	require.NoError(t, os.MkdirAll(cacheDir, 0o755))
	file := filepath.Join(cacheDir, "test.txt")
	require.NoError(t, os.WriteFile(file, []byte("data"), 0o644))

	called := false

	opts := Options{
		Version:          "test-version",
		CacheDir:         cacheDir,
		TempDirBase:      os.TempDir(),
		ExternalDiffTool: "",
		InitLogging:      func(bool) {},
		RunApp: func(app.Config) error {
			called = true
			return nil
		},
	}

	err := Execute(opts, []string{"--drop-cache"})
	require.NoError(t, err)

	_, statErr := os.Stat(cacheDir)
	assert.True(t, os.IsNotExist(statErr))
	assert.False(t, called, "run function should not execute when dropping cache")
}

func TestExecuteErrorScenarios(t *testing.T) {
	cases := []struct {
		name      string
		setupOpts func(t *testing.T) Options
		args      []string
		wantErr   string
	}{
		{
			name: "missing run handler",
			setupOpts: func(t *testing.T) Options {
				return Options{
					Version:          "test",
					CacheDir:         t.TempDir(),
					TempDirBase:      t.TempDir(),
					ExternalDiffTool: "",
					InitLogging:      func(bool) {},
					RunApp:           nil,
				}
			},
			args:    []string{"branch", "main"},
			wantErr: "no run handler provided",
		},
		{
			name: "run handler failure",
			setupOpts: func(t *testing.T) Options {
				return Options{
					Version:          "test",
					CacheDir:         t.TempDir(),
					TempDirBase:      t.TempDir(),
					ExternalDiffTool: "",
					InitLogging:      func(bool) {},
					RunApp: func(app.Config) error {
						return errors.New("execution failed")
					},
				}
			},
			args:    []string{"branch", "main"},
			wantErr: "execution failed",
		},
		{
			name: "missing branch argument",
			setupOpts: func(t *testing.T) Options {
				return Options{
					Version:          "test",
					CacheDir:         t.TempDir(),
					TempDirBase:      t.TempDir(),
					ExternalDiffTool: "",
					InitLogging:      func(bool) {},
					RunApp: func(app.Config) error {
						t.Fatalf("RunApp should not be called")
						return nil
					},
				}
			},
			args:    []string{"branch"},
			wantErr: "accepts 1 arg(s), received 0",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.setupOpts(t)
			err := Execute(opts, tc.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
