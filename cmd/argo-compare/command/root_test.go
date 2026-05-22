package command

import (
	"context"
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
		RunApp: func(_ context.Context, cfg app.Config) error {
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
		RunApp: func(_ context.Context, cfg app.Config) error {
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
		RunApp: func(_ context.Context, _ app.Config) error {
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
					RunApp: func(_ context.Context, _ app.Config) error {
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
					RunApp: func(_ context.Context, _ app.Config) error {
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

func TestExecuteUsesGitLabCIEnvDefaults(t *testing.T) {
	var receivedConfig app.Config

	opts := Options{
		Version:          "test-version",
		CacheDir:         t.TempDir(),
		TempDirBase:      os.TempDir(),
		ExternalDiffTool: "",
		InitLogging:      func(bool) {},
		RunApp: func(_ context.Context, cfg app.Config) error {
			receivedConfig = cfg
			return nil
		},
	}

	t.Setenv("GITLAB_CI", "true")
	t.Setenv("CI_MERGE_REQUEST_IID", "42")
	t.Setenv("CI_PROJECT_ID", "321")
	t.Setenv("CI_SERVER_URL", "https://gitlab.example.com")
	t.Setenv("CI_JOB_TOKEN", "job-token")

	err := Execute(opts, []string{"branch", "main"})
	require.NoError(t, err)

	require.NotNil(t, receivedConfig.Comment)
	assert.Equal(t, app.CommentProviderGitLab, receivedConfig.Comment.Provider)
	assert.Equal(t, "https://gitlab.example.com", receivedConfig.Comment.GitLab.BaseURL)
	assert.Equal(t, "321", receivedConfig.Comment.GitLab.ProjectID)
	assert.Equal(t, 42, receivedConfig.Comment.GitLab.MergeRequestIID)
	assert.Equal(t, "job-token", receivedConfig.Comment.GitLab.Token)
}

func TestExecuteValidationFlags(t *testing.T) {
	var receivedConfig app.Config

	opts := Options{
		Version:          "test-version",
		CacheDir:         t.TempDir(),
		TempDirBase:      os.TempDir(),
		ExternalDiffTool: "",
		InitLogging:      func(bool) {},
		RunApp: func(_ context.Context, cfg app.Config) error {
			receivedConfig = cfg
			return nil
		},
	}

	args := []string{
		"branch", "main",
		"--validate-manifests",
		"--kubeconform-path", "/usr/local/bin/kubeconform",
		"--skip-validation-kinds", "ServiceMonitor,ArgoApplication",
		"--schema-location", "https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json",
		"--schema-location", "schemas/{{.ResourceKind}}{{.KindSuffix}}.json",
	}

	err := Execute(opts, args)
	require.NoError(t, err)

	assert.True(t, receivedConfig.ValidateManifests)
	assert.Equal(t, "/usr/local/bin/kubeconform", receivedConfig.KubeconformPath)
	assert.Equal(t, []string{"ServiceMonitor", "ArgoApplication"}, receivedConfig.ValidateSkipKinds)
	assert.Equal(t,
		[]string{
			"https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json",
			"schemas/{{.ResourceKind}}{{.KindSuffix}}.json",
		},
		receivedConfig.ValidateSchemaLocations,
	)
}

func TestExecuteValidationDisabledByDefault(t *testing.T) {
	var receivedConfig app.Config

	opts := Options{
		Version:          "test-version",
		CacheDir:         t.TempDir(),
		TempDirBase:      os.TempDir(),
		ExternalDiffTool: "",
		InitLogging:      func(bool) {},
		RunApp: func(_ context.Context, cfg app.Config) error {
			receivedConfig = cfg
			return nil
		},
	}

	err := Execute(opts, []string{"branch", "main"})
	require.NoError(t, err)

	assert.False(t, receivedConfig.ValidateManifests)
	assert.Empty(t, receivedConfig.KubeconformPath)
	assert.Empty(t, receivedConfig.ValidateSkipKinds)
	assert.Empty(t, receivedConfig.ValidateSchemaLocations)
}

func TestExecuteValidationEnvVars(t *testing.T) {
	var receivedConfig app.Config

	opts := Options{
		Version:          "test-version",
		CacheDir:         t.TempDir(),
		TempDirBase:      os.TempDir(),
		ExternalDiffTool: "",
		InitLogging:      func(bool) {},
		RunApp: func(_ context.Context, cfg app.Config) error {
			receivedConfig = cfg
			return nil
		},
	}

	t.Setenv("ARGO_COMPARE_VALIDATE_MANIFESTS", "true")
	t.Setenv("ARGO_COMPARE_KUBECONFORM_PATH", "/opt/kubeconform")
	t.Setenv("ARGO_COMPARE_SKIP_VALIDATION_KINDS", "CustomResource,AnotherKind")
	t.Setenv("ARGO_COMPARE_KUBECONFORM_SCHEMA_LOCATIONS",
		"https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json, schemas/{{.ResourceKind}}{{.KindSuffix}}.json")

	err := Execute(opts, []string{"branch", "main"})
	require.NoError(t, err)

	assert.True(t, receivedConfig.ValidateManifests)
	assert.Equal(t, "/opt/kubeconform", receivedConfig.KubeconformPath)
	assert.Equal(t, []string{"CustomResource", "AnotherKind"}, receivedConfig.ValidateSkipKinds)
	assert.Equal(t,
		[]string{
			"https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json",
			"schemas/{{.ResourceKind}}{{.KindSuffix}}.json",
		},
		receivedConfig.ValidateSchemaLocations,
	)
}

func TestExecuteValidationEnvVarFalsyValues(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"false", "false"},
		{"zero", "0"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var receivedConfig app.Config

			opts := Options{
				Version:          "test-version",
				CacheDir:         t.TempDir(),
				TempDirBase:      os.TempDir(),
				ExternalDiffTool: "",
				InitLogging:      func(bool) {},
				RunApp: func(_ context.Context, cfg app.Config) error {
					receivedConfig = cfg
					return nil
				},
			}

			t.Setenv("ARGO_COMPARE_VALIDATE_MANIFESTS", tc.value)

			err := Execute(opts, []string{"branch", "main"})
			require.NoError(t, err)

			assert.False(t, receivedConfig.ValidateManifests)
		})
	}
}

func TestExecuteValidationCLIFlagsBeatEnvVars(t *testing.T) {
	// Documented in README: when both are set, CLI flags take precedence.
	var receivedConfig app.Config

	opts := Options{
		Version:          "test-version",
		CacheDir:         t.TempDir(),
		TempDirBase:      os.TempDir(),
		ExternalDiffTool: "",
		InitLogging:      func(bool) {},
		RunApp: func(_ context.Context, cfg app.Config) error {
			receivedConfig = cfg
			return nil
		},
	}

	// Env vars seed one set of values...
	t.Setenv("ARGO_COMPARE_VALIDATE_MANIFESTS", "false")
	t.Setenv("ARGO_COMPARE_KUBECONFORM_PATH", "/from/env/kubeconform")
	t.Setenv("ARGO_COMPARE_SKIP_VALIDATION_KINDS", "FromEnvKind")
	t.Setenv("ARGO_COMPARE_KUBECONFORM_SCHEMA_LOCATIONS", "schemas/from-env/{{.ResourceKind}}.json")

	// ...CLI flags pass distinct, conflicting values.
	args := []string{
		"branch", "main",
		"--validate-manifests",
		"--kubeconform-path", "/from/cli/kubeconform",
		"--skip-validation-kinds", "FromCliKind,SecondCliKind",
		"--schema-location", "schemas/from-cli/{{.ResourceKind}}.json",
	}

	require.NoError(t, Execute(opts, args))

	assert.True(t, receivedConfig.ValidateManifests, "CLI --validate-manifests should override env=false")
	assert.Equal(t, "/from/cli/kubeconform", receivedConfig.KubeconformPath, "CLI path should win")
	assert.Equal(t, []string{"FromCliKind", "SecondCliKind"}, receivedConfig.ValidateSkipKinds, "CLI kinds should win")
	assert.Equal(t, []string{"schemas/from-cli/{{.ResourceKind}}.json"}, receivedConfig.ValidateSchemaLocations,
		"CLI schema locations should win")
}
