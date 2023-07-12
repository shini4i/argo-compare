package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alecthomas/kong"
	"github.com/fatih/color"
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/shini4i/argo-compare/internal/models"
	"os"
	"strings"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
)

const (
	loggerName      = "argo-compare"
	repoCredsPrefix = "REPO_CREDS_"
)

var (
	cacheDir        = helpers.GetEnv("ARGO_COMPARE_CACHE_DIR", fmt.Sprintf("%s/.cache/argo-compare", os.Getenv("HOME")))
	tmpDir          string
	version         = "local"
	repo            = GitRepo{CmdRunner: &utils.RealCmdRunner{}}
	repoCredentials []RepoCredentials
	diffCommand     = helpers.GetEnv("ARGO_COMPARE_DIFF_COMMAND", "built-in")
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
	failedToDownloadChart = errors.New("failed to download chart")
)

var (
	cyan = color.New(color.FgCyan, color.Bold).SprintFunc()
)

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

func processFiles(cmdRunner utils.CmdRunner, fileName string, fileType string, application models.Application) error {
	log.Debugf("Processing [%s] file: [%s]", cyan(fileType), cyan(fileName))

	target := Target{CmdRunner: cmdRunner, FileReader: utils.OsFileReader{}, File: fileName, Type: fileType, App: application}
	if fileType == "src" {
		if err := target.parse(); err != nil {
			return err
		}
	}

	target.generateValuesFiles()
	if err := target.ensureHelmCharts(); err != nil {
		return err
	}

	target.extractCharts()
	target.renderAppSources()

	return nil
}

func compareFiles(cmdRunner utils.CmdRunner, changedFiles []string) {
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

			if err = processFiles(cmdRunner, file, "src", models.Application{}); err != nil {
				log.Panicf("Could not process the source Application: %s", err)
			}

			app, err := repo.getChangedFileContent(targetBranch, file)
			if errors.Is(err, gitFileDoesNotExist) && !printAddedManifests {
				return
			} else if err != nil && !errors.Is(err, models.EmptyFileError) {
				log.Errorf("Could not get the target Application from branch [%s]: %s", targetBranch, err)
			}

			if !errors.Is(err, models.EmptyFileError) {
				if err = processFiles(cmdRunner, file, "dst", app); err != nil && !printAddedManifests {
					log.Panicf("Could not process the destination Application: %s", err)
				}
			}

			comparer := Compare{
				CmdRunner: &utils.RealCmdRunner{},
			}
			comparer.findFiles()
			comparer.printFilesStatus()
		}()
	}
}

func collectRepoCredentials() error {
	log.Debug("===> Collecting repo credentials")
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, repoCredsPrefix) {
			var repoCreds RepoCredentials
			if err := json.Unmarshal([]byte(strings.SplitN(env, "=", 2)[1]), &repoCreds); err != nil {
				return err
			}
			repoCredentials = append(repoCredentials, repoCreds)
		}
	}

	for _, repo := range repoCredentials {
		log.Debugf("▶ Found repo credentials for [%s]", cyan(repo.Url))
	}

	return nil
}

func printInvalidFilesList(repo *GitRepo) error {
	if len(repo.invalidFiles) > 0 {
		log.Info("===> The following yaml files are invalid and were skipped")
		for _, file := range repo.invalidFiles {
			log.Warningf("▶ %s", file)
		}
		return errors.New("invalid files found")
	}
	return nil
}

// parseCli processes command-line arguments, setting appropriate global variables based on user input.
// If the user does not provide a recognized command, it returns an error.
func parseCli() error {
	ctx := kong.Parse(&CLI,
		kong.Name("argo-compare"),
		kong.Description("Compare ArgoCD applications between git branches"),
		kong.UsageOnError(),
		kong.Vars{"version": version})

	switch ctx.Command() {
	case "branch <name>":
		targetBranch = CLI.Branch.Name
		fileToCompare = CLI.Branch.File
	}

	return nil
}

func runCLI() error {
	if err := parseCli(); err != nil {
		return err
	}

	updateConfigurations()

	log.Infof("===> Running Argo Compare version [%s]", cyan(version))

	if err := collectRepoCredentials(); err != nil {
		return err
	}

	changedFiles, err := getChangedFiles(utils.OsFileReader{}, &repo, fileToCompare)
	if err != nil {
		return err
	}

	if len(changedFiles) == 0 {
		log.Info("No changed Application files found. Exiting...")
	} else {
		compareFiles(&utils.RealCmdRunner{}, changedFiles)
	}

	return printInvalidFilesList(&repo)
}

func getChangedFiles(fileReader utils.FileReader, repo *GitRepo, fileToCompare string) ([]string, error) {
	var changedFiles []string
	var err error

	if fileToCompare != "" {
		changedFiles = []string{fileToCompare}
	} else {
		changedFiles, err = repo.getChangedFiles(fileReader)
		if err != nil {
			return nil, err
		}
	}

	return changedFiles, nil
}

func updateConfigurations() {
	if CLI.Debug {
		loggingInit(logging.DEBUG)
	} else {
		loggingInit(logging.INFO)
	}

	if CLI.Branch.PreserveHelmLabels {
		preserveHelmLabels = true
	}

	if CLI.Branch.PrintAddedManifests {
		printAddedManifests = true
	}

	if CLI.Branch.PrintRemovedManifests {
		printRemovedManifests = true
	}

	if CLI.Branch.FullOutput {
		printAddedManifests = true
		printRemovedManifests = true
	}
}

func main() {
	if err := runCLI(); err != nil {
		log.Fatal(err)
	}
}
