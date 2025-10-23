package main

import (
	"fmt"
	"os"

	"github.com/op/go-logging"
	command "github.com/shini4i/argo-compare/cmd/argo-compare/command"
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

	runApp := func(cfg app.Config) error {
		deps := setupDependencies(log)
		application, err := app.New(cfg, deps)
		if err != nil {
			return err
		}
		return application.Run()
	}

	opts := command.Options{
		Version:          version,
		CacheDir:         cacheDir,
		TempDirBase:      os.TempDir(),
		ExternalDiffTool: os.Getenv("EXTERNAL_DIFF_TOOL"),
		RunApp:           runApp,
		InitLogging:      loggingInit,
	}

	if err := command.Execute(opts, nil); err != nil {
		log.Fatal(err)
	}
}
