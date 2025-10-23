package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/app"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/spf13/afero"
)

const loggerName = "argo-compare"

var (
	version  = "local"
	cacheDir string

	log    = logging.MustGetLogger(loggerName)
	format = logging.MustStringFormatter(`%{message}`)
)

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

func buildConfig() app.Config {
	printAdded := CLI.Branch.PrintAddedManifests
	printRemoved := CLI.Branch.PrintRemovedManifests

	if CLI.Branch.FullOutput {
		printAdded = true
		printRemoved = true
	}

	return app.Config{
		TargetBranch:          CLI.Branch.Name,
		FileToCompare:         CLI.Branch.File,
		FilesToIgnore:         CLI.Branch.Ignore,
		PreserveHelmLabels:    CLI.Branch.PreserveHelmLabels,
		PrintAddedManifests:   printAdded,
		PrintRemovedManifests: printRemoved,
		CacheDir:              cacheDir,
		TempDirBase:           os.TempDir(),
		ExternalDiffTool:      os.Getenv("EXTERNAL_DIFF_TOOL"),
		Debug:                 CLI.Debug,
		Version:               version,
	}
}

func setupDependencies(logger *logging.Logger) app.Dependencies {
	return app.Dependencies{
		FS:            afero.NewOsFs(),
		CmdRunner:     &utils.RealCmdRunner{},
		FileReader:    utils.OsFileReader{},
		HelmProcessor: utils.RealHelmChartProcessor{Log: logger},
		Globber:       utils.CustomGlobber{},
		Logger:        logger,
	}
}

func main() {
	cacheDir = helpers.GetEnv("ARGO_COMPARE_CACHE_DIR", fmt.Sprintf("%s/.cache/argo-compare", os.Getenv("HOME")))

	kong.Parse(&CLI,
		kong.Name("argo-compare"),
		kong.Description("Compare ArgoCD applications between git branches"),
		kong.UsageOnError(),
		kong.Vars{"version": version})

	config := buildConfig()

	loggingInit(config.Debug)

	application, err := app.New(config, setupDependencies(log))
	if err != nil {
		log.Fatal(err)
	}

	if err := application.Run(); err != nil {
		log.Fatal(err)
	}
}
