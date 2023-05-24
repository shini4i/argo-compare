package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alecthomas/kong"
	"github.com/fatih/color"
	"github.com/op/go-logging"
	h "github.com/shini4i/argo-compare/internal/helpers"
	m "github.com/shini4i/argo-compare/internal/models"
	"os"
	"os/exec"
	"strings"
)

const (
	loggerName      = "argo-compare"
	repoCredsPrefix = "REPO_CREDS_"
)

var (
	cacheDir        = h.GetEnv("ARGO_COMPARE_CACHE_DIR", fmt.Sprintf("%s/.cache/argo-compare", os.Getenv("HOME")))
	tmpDir          string
	version         = "local"
	repo            = GitRepo{}
	repoCredentials []RepoCredentials
	diffCommand     = h.GetEnv("ARGO_COMPARE_DIFF_COMMAND", "built-in")
)

var (
	log = logging.MustGetLogger(loggerName)
	// A bit weird, but it seems that it's the easiest way to implement log level support in CLI tool
	// without printing the log level and timestamp in the output
	format = logging.MustStringFormatter(
		`%{message}`,
	)
)

var (
	unsupportedAppConfiguration = errors.New("unsupported application configuration")
	failedToDownloadChart       = errors.New("failed to download chart")
)

var (
	cyan = color.New(color.FgCyan, color.Bold).SprintFunc()
)

type execContext = func(name string, arg ...string) *exec.Cmd

type RepoCredentials struct {
	Url      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func loggingInit(level logging.Level) {
	backend := logging.NewLogBackend(os.Stdout, "", 0)
	backendFormatter := logging.NewBackendFormatter(backend, format)
	logging.SetBackend(backendFormatter)
	logging.SetLevel(level, "")
}

func processFiles(fileName string, fileType string, application m.Application) error {
	log.Debugf("Processing [%s] file: [%s]", cyan(fileType), cyan(fileName))

	target := Target{File: fileName, Type: fileType, App: application}
	if fileType == "src" {
		if err := target.parse(); err != nil {
			return err
		}
	}

	target.generateValuesFiles()
	if err := target.collectHelmChart(); err != nil {
		return err
	}

	target.extractChart()
	target.renderAppSources()

	return nil
}

func compareFiles(changedFiles []string) {
	for _, file := range changedFiles {
		// We want to make sure that the temporary directory is removed after each iteration
		// whatever the result is, and not after the whole loop is finished, hence the anonymous function
		func() {
			var err error

			log.Infof("===> Processing changed application: [%s]", cyan(file))

			if tmpDir, err = os.MkdirTemp("/tmp", "argo-compare-*"); err != nil {
				log.Panic(err)
			}

			defer func(path string) {
				err := os.RemoveAll(path)
				if err != nil {
					log.Panic(err)
				}
			}(tmpDir)

			if err = processFiles(file, "src", m.Application{}); err == unsupportedAppConfiguration {
				color.Yellow("Skipping unsupported application configuration")
				return
			} else if err != nil {
				log.Panicf("Could not process the source Application: %s", err)
			}

			app, err := repo.getChangedFileContent(targetBranch, file, exec.Command)
			if err == gitFileDoesNotExist && !printAddedManifests {
				return
			} else if err != nil {
				log.Panicf("Could not get the target Application from branch [%s]: %s", targetBranch, err)
			}

			if err = processFiles(file, "dst", app); err != nil && !printAddedManifests {
				log.Panicf("Could not process the destination Application: %s", err)
			}

			comparer := Compare{}
			comparer.findFiles()
			comparer.printFilesStatus()
		}()
	}
}

func collectRepoCredentials() {
	log.Debug("===> Collecting repo credentials")
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, repoCredsPrefix) {
			var repoCreds RepoCredentials
			if err := json.Unmarshal([]byte(strings.SplitN(env, "=", 2)[1]), &repoCreds); err != nil {
				log.Fatal(err)
			}
			repoCredentials = append(repoCredentials, repoCreds)
		}
	}

	for _, repo := range repoCredentials {
		log.Debugf("▶ Found repo credentials for [%s]", cyan(repo.Url))
	}
}

func printInvalidFilesList() {
	if len(repo.invalidFiles) > 0 {
		log.Info("===> The following yaml files are invalid and were skipped")
		for _, file := range repo.invalidFiles {
			log.Warningf("▶ %s", file)
		}
		os.Exit(1)
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

	// Cover the edge case when we need to render all manifests for a new Application
	// It will produce a big output and does not fit into "compare" definition hence it's disabled by default
	if CLI.Branch.PrintAddedManifests {
		printAddedManifests = true
	}

	// Cover cases when we need to print out all removed manifests
	if CLI.Branch.PrintRemovedManifests {
		printRemovedManifests = true
	}

	// Cover cases when we need to print out all manifests. It will produce the biggest output.
	if CLI.Branch.FullOutput {
		printAddedManifests = true
		printRemovedManifests = true
	}

	log.Infof("===> Running argo-compare version [%s]", cyan(version))

	collectRepoCredentials()

	var changedFiles []string
	var err error

	// There are valid cases when we want to compare a single file only
	if fileToCompare != "" {
		changedFiles = []string{fileToCompare}
	} else {
		if changedFiles, err = repo.getChangedFiles(exec.Command); err != nil {
			log.Fatal(err)
		}
	}

	if len(changedFiles) == 0 {
		log.Info("No changed Application files found. Exiting...")
	} else {
		compareFiles(changedFiles)
	}

	printInvalidFilesList()
}
