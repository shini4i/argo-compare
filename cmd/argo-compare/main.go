package main

import (
	"errors"
	"fmt"
	"github.com/alecthomas/kong"
	h "github.com/shini4i/argo-compare/internal/helpers"
	m "github.com/shini4i/argo-compare/internal/models"
	"os"
	"os/exec"

	"github.com/op/go-logging"
)

const loggerName = "argo-compare"

var (
	targetBranch       string
	fileToCompare      string
	cacheDir           = fmt.Sprintf("%s/.cache/argo-compare", os.Getenv("HOME"))
	tmpDir             string
	version            = "local"
	repo               = GitRepo{}
	diffCommand        = h.GetEnv("ARGO_COMPARE_DIFF_COMMAND", "built-in")
	preserveHelmLabels bool
)

var (
	log = logging.MustGetLogger(loggerName)
	// A bit weird, but it seems that it's the easiest way to implement log level support in CLI tool
	// without printing the log level and timestamp in the output
	format = logging.MustStringFormatter(
		`%{color}%{message}%{color:reset}`,
	)
)

var CLI struct {
	Debug   bool             `help:"Enable debug mode" short:"d"`
	Version kong.VersionFlag `help:"Show version" short:"v"`

	Branch struct {
		Name               string `arg:"" type:"string"`
		File               string `help:"Compare a single file" short:"f"`
		PreserveHelmLabels bool   `help:"Preserve Helm labels"`
	} `cmd:"" help:"target branch to compare with" type:"string"`
}

type execContext = func(name string, arg ...string) *exec.Cmd

func loggingInit(level logging.Level) {
	backend := logging.NewLogBackend(os.Stdout, "", 0)
	backendFormatter := logging.NewBackendFormatter(backend, format)
	logging.SetBackend(backendFormatter)
	logging.SetLevel(level, "")
}

func processFiles(fileName string, fileType string, application m.Application) error {
	log.Debugf("Processing [%s] file: [%s]", fileType, fileName)

	app := Application{File: fileName, Type: fileType, App: application}
	if fileType == "src" {
		if err := app.parse(); err != nil {
			return err
		}
	}

	if len(app.App.Spec.Source.Chart) == 0 {
		return errors.New("unsupported application configuration")
	}

	app.writeValuesYaml()
	if err := app.collectHelmChart(); err != nil {
		return err
	}

	app.extractChart()
	app.renderTemplate()

	return nil
}

func compareFiles(changedFiles []string) {
	for _, file := range changedFiles {
		var err error

		log.Infof("===> Processing changed application: [%s]", file)

		tmpDir, err = os.MkdirTemp("/tmp", "argo-compare-*")
		if err != nil {
			log.Fatal(err)
		}

		if err = processFiles(file, "src", m.Application{}); err != nil {
			log.Fatalf("Could not process the source Application: %s", err)
			continue
		}

		app, err := repo.getChangedFileContent(targetBranch, file, exec.Command)
		if err != nil {
			log.Debugf("Could not get the target Application from branch [%s]: %s", targetBranch, err)
			continue
		}

		if err = processFiles(file, "dst", app); err != nil {
			log.Fatalf("Could not process the destination Application: %s", err)
			continue
		}

		comparer := Compare{}
		comparer.findFiles()
		comparer.printCompareResults()

		err = os.RemoveAll(tmpDir)
		if err != nil {
			log.Fatal(err.Error())
		}
	}
}

func main() {
	ctx := kong.Parse(&CLI,
		kong.Name("argo-compare"),
		kong.Description("Compare ArgoCD applications between git branches"),
		kong.UsageOnError(),
		kong.Vars{"version": version})

	switch ctx.Command() {
	case "branch <name>":
		targetBranch = CLI.Branch.Name
		if len(CLI.Branch.File) > 0 {
			fileToCompare = CLI.Branch.File
		}
	default:
		panic(ctx.Command())
	}

	if CLI.Debug {
		loggingInit(logging.DEBUG)
	} else {
		loggingInit(logging.INFO)
	}

	if CLI.Branch.PreserveHelmLabels {
		preserveHelmLabels = true
	}

	log.Infof("===> Running argo-compare version [%s]", version)

	var changedFiles []string
	var err error

	// There are valid cases when we want to compare a single file only
	if fileToCompare != "" {
		changedFiles = []string{fileToCompare}
	} else {
		if changedFiles, err = repo.getChangedFiles(exec.Command); err != nil {
			panic(err)
		}
		if len(changedFiles) == 0 {
			log.Info("No changed Application files found. Exiting...")
			os.Exit(0)
		}
	}

	compareFiles(changedFiles)

	if len(repo.invalidFiles) > 0 {
		log.Info("===> The following yaml files are invalid and were skipped")
		for _, file := range repo.invalidFiles {
			log.Warningf("??? %s", file)
		}
		os.Exit(1)
	}
}
