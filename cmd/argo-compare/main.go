// Package main is the entry point for the argo-compare CLI tool that compares
// ArgoCD Application manifests between git branches and displays the differences.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/op/go-logging"
	command "github.com/shini4i/argo-compare/cmd/argo-compare/command"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/app"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/shini4i/argo-compare/internal/sanitizer"
	"github.com/spf13/afero"
)

const loggerName = "argo-compare"

var (
	version = "local"

	log    = logging.MustGetLogger(loggerName)
	format = logging.MustStringFormatter(`%{message}`)
)

// loggingInit configures global logging verbosity and output formatting.
func loggingInit(debug bool) {
	level := logging.INFO
	if debug {
		level = logging.DEBUG
	}

	backend := logging.NewLogBackend(os.Stdout, "", 0)
	backendFormatter := logging.NewBackendFormatter(backend, format)
	logging.SetBackend(backendFormatter)
	logging.SetLevel(level, "")
}

// setupDependencies wires runtime collaborators used by the application.
func setupDependencies(logger *logging.Logger) app.Dependencies {
	return app.Dependencies{
		FS:                  afero.NewOsFs(),
		CmdRunner:           &utils.RealCmdRunner{},
		FileReader:          utils.OsFileReader{},
		HelmProcessor:       utils.RealHelmChartProcessor{Log: logger},
		Globber:             utils.CustomGlobber{},
		Logger:              logger,
		SensitiveDataMasker: sanitizer.NewKubernetesSecretMasker(),
	}
}

// main is the entry point for the argo-compare CLI binary.
func main() {
	opts := buildOptions()

	if err := command.Execute(opts, nil); err != nil {
		log.Fatal(err)
	}
}

// buildOptions assembles the command execution options from environment defaults.
func buildOptions() command.Options {
	return command.Options{
		Version:          version,
		CacheDir:         resolveCacheDir(),
		TempDirBase:      os.TempDir(),
		ExternalDiffTool: os.Getenv("EXTERNAL_DIFF_TOOL"),
		RunApp:           runApplication,
		InitLogging:      loggingInit,
	}
}

// resolveCacheDir returns the cache directory path, using the ARGO_COMPARE_CACHE_DIR
// environment variable if set; otherwise it defaults to "$HOME/.cache/argo-compare".
func resolveCacheDir() string {
	return helpers.GetEnv("ARGO_COMPARE_CACHE_DIR", fmt.Sprintf("%s/.cache/argo-compare", os.Getenv("HOME")))
}

// runApplication constructs and executes the application using the supplied configuration.
// runApplication creates application dependencies, instantiates the app with the provided config, and runs it using ctx for cancellation and timeouts.
// It returns any error encountered while constructing or running the application.
func runApplication(ctx context.Context, cfg app.Config) error {
	deps := setupDependencies(log)
	application, err := app.New(cfg, deps)
	if err != nil {
		return err
	}
	return application.Run(ctx)
}
