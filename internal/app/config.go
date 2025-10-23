package app

import (
	"errors"
	"os"
)

// Config captures runtime parameters for a comparison run.
type Config struct {
	TargetBranch          string
	FileToCompare         string
	FilesToIgnore         []string
	PreserveHelmLabels    bool
	PrintAddedManifests   bool
	PrintRemovedManifests bool
	CacheDir              string
	TempDirBase           string
	ExternalDiffTool      string
	Debug                 bool
	Version               string
}

// ConfigOption mutates a Config during construction.
type ConfigOption func(*Config)

// NewConfig creates a Config with defaults and applies provided options.
func NewConfig(targetBranch string, opts ...ConfigOption) (Config, error) {
	if targetBranch == "" {
		return Config{}, errors.New("target branch must be provided")
	}

	cfg := Config{
		TargetBranch: targetBranch,
		TempDirBase:  os.TempDir(),
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg, nil
}

// WithFileToCompare sets the specific manifest file to inspect.
func WithFileToCompare(file string) ConfigOption {
	return func(cfg *Config) {
		cfg.FileToCompare = file
	}
}

// WithFilesToIgnore configures manifest paths that should be skipped.
func WithFilesToIgnore(files []string) ConfigOption {
	return func(cfg *Config) {
		cfg.FilesToIgnore = append([]string{}, files...)
	}
}

// WithPreserveHelmLabels toggles stripping of Helm-managed labels.
func WithPreserveHelmLabels(enabled bool) ConfigOption {
	return func(cfg *Config) {
		cfg.PreserveHelmLabels = enabled
	}
}

// WithPrintAdded determines whether newly added manifests are rendered.
func WithPrintAdded(enabled bool) ConfigOption {
	return func(cfg *Config) {
		cfg.PrintAddedManifests = enabled
	}
}

// WithPrintRemoved determines whether removed manifests are rendered.
func WithPrintRemoved(enabled bool) ConfigOption {
	return func(cfg *Config) {
		cfg.PrintRemovedManifests = enabled
	}
}

// WithCacheDir overrides the cache directory used for Helm charts.
func WithCacheDir(path string) ConfigOption {
	return func(cfg *Config) {
		cfg.CacheDir = path
	}
}

// WithTempDirBase overrides the base directory for temporary workspaces.
func WithTempDirBase(path string) ConfigOption {
	return func(cfg *Config) {
		if path != "" {
			cfg.TempDirBase = path
		}
	}
}

// WithExternalDiffTool specifies an external diff viewer to launch.
func WithExternalDiffTool(tool string) ConfigOption {
	return func(cfg *Config) {
		cfg.ExternalDiffTool = tool
	}
}

// WithDebug toggles verbose logging.
func WithDebug(enabled bool) ConfigOption {
	return func(cfg *Config) {
		cfg.Debug = enabled
	}
}

// WithVersion sets the application version used in log output.
func WithVersion(version string) ConfigOption {
	return func(cfg *Config) {
		cfg.Version = version
	}
}
