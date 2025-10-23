package app

import (
	"errors"
	"os"
)

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

type ConfigOption func(*Config)

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

func WithFileToCompare(file string) ConfigOption {
	return func(cfg *Config) {
		cfg.FileToCompare = file
	}
}

func WithFilesToIgnore(files []string) ConfigOption {
	return func(cfg *Config) {
		cfg.FilesToIgnore = append([]string{}, files...)
	}
}

func WithPreserveHelmLabels(enabled bool) ConfigOption {
	return func(cfg *Config) {
		cfg.PreserveHelmLabels = enabled
	}
}

func WithPrintAdded(enabled bool) ConfigOption {
	return func(cfg *Config) {
		cfg.PrintAddedManifests = enabled
	}
}

func WithPrintRemoved(enabled bool) ConfigOption {
	return func(cfg *Config) {
		cfg.PrintRemovedManifests = enabled
	}
}

func WithCacheDir(path string) ConfigOption {
	return func(cfg *Config) {
		cfg.CacheDir = path
	}
}

func WithTempDirBase(path string) ConfigOption {
	return func(cfg *Config) {
		if path != "" {
			cfg.TempDirBase = path
		}
	}
}

func WithExternalDiffTool(tool string) ConfigOption {
	return func(cfg *Config) {
		cfg.ExternalDiffTool = tool
	}
}

func WithDebug(enabled bool) ConfigOption {
	return func(cfg *Config) {
		cfg.Debug = enabled
	}
}

func WithVersion(version string) ConfigOption {
	return func(cfg *Config) {
		cfg.Version = version
	}
}
