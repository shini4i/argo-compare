package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/fatih/color"
	"github.com/op/go-logging"
	interfaces "github.com/shini4i/argo-compare/cmd/argo-compare/interfaces"
	"github.com/spf13/afero"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/shini4i/argo-compare/internal/models"
)

const (
	loggerName      = "argo-compare"
	repoCredsPrefix = "REPO_CREDS_" // #nosec G101
)

var (
	cacheDir         = helpers.GetEnv("ARGO_COMPARE_CACHE_DIR", fmt.Sprintf("%s/.cache/argo-compare", os.Getenv("HOME")))
	tmpDir           string
	version          = "local"
	repoCredentials  []models.RepoCredentials
	externalDiffTool = os.Getenv("EXTERNAL_DIFF_TOOL")
)

var (
	log = logging.MustGetLogger(loggerName)
	// A bit weird, but it seems that it's the easiest way to implement log level support in CLI tool
	// without printing the log level and timestamp in the output.
	format = logging.MustStringFormatter(
		`%{message}`,
	)
	helmChartProcessor = utils.RealHelmChartProcessor{Log: log}
)

var (
	cyan   = color.New(color.FgCyan, color.Bold).SprintFunc()
	red    = color.New(color.FgRed, color.Bold).SprintFunc()
	yellow = color.New(color.FgYellow, color.Bold).SprintFunc()
)

func loggingInit(level logging.Level) {
	backend := logging.NewLogBackend(os.Stdout, "", 0)
	backendFormatter := logging.NewBackendFormatter(backend, format)
	logging.SetBackend(backendFormatter)
	logging.SetLevel(level, "")
}

func processFiles(cmdRunner interfaces.CmdRunner, fileName string, fileType string, application models.Application) error {
	log.Debugf("Processing [%s] file: [%s]", cyan(fileType), cyan(fileName))

	target := Target{CmdRunner: cmdRunner, FileReader: utils.OsFileReader{}, File: fileName, Type: fileType, App: application}
	if fileType == "src" {
		if err := target.parse(); err != nil {
			return err
		}
	}

	if err := target.generateValuesFiles(helmChartProcessor); err != nil {
		return err
	}

	if err := target.ensureHelmCharts(helmChartProcessor); err != nil {
		return err
	}

	if err := target.extractCharts(helmChartProcessor); err != nil {
		return err
	}

	if err := target.renderAppSources(helmChartProcessor); err != nil {
		return err
	}

	return nil
}

func compareFiles(fs afero.Fs, cmdRunner interfaces.CmdRunner, repo *GitRepo, changedFiles []string) {
	for _, file := range changedFiles {
		var err error

		log.Infof("===> Processing changed application: [%s]", cyan(file))

		if tmpDir, err = afero.TempDir(fs, "/tmp", "argo-compare-"); err != nil {
			log.Panic(err)
		}

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

		runComparison(cmdRunner)

		if err := fs.RemoveAll(tmpDir); err != nil {
			log.Panic(err)
		}
	}
}

func runComparison(cmdRunner interfaces.CmdRunner) {
	comparer := Compare{
		CmdRunner:        cmdRunner,
		externalDiffTool: externalDiffTool,
	}
	comparer.findFiles()
	comparer.printFilesStatus()
}

func collectRepoCredentials() error {
	log.Debug("===> Collecting repo credentials")
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, repoCredsPrefix) {
			var repoCreds models.RepoCredentials
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
		filesToIgnore = CLI.Branch.Ignore
	}

	return nil
}

func runCLI() error {
	if err := parseCli(); err != nil {
		return err
	}

	updateConfigurations()

	repo, err := NewGitRepo(afero.NewOsFs(), &utils.RealCmdRunner{})
	if err != nil {
		return err
	}

	log.Infof("===> Running Argo Compare version [%s]", cyan(version))

	if err := collectRepoCredentials(); err != nil {
		return err
	}

	changedFiles, err := getChangedFiles(repo, fileToCompare, filesToIgnore)
	if err != nil {
		return err
	}

	if len(changedFiles) == 0 {
		log.Info("No changed Application files found. Exiting...")
	} else {
		compareFiles(afero.NewOsFs(), &utils.RealCmdRunner{}, repo, changedFiles)
	}

	return printInvalidFilesList(repo)
}

func getChangedFiles(repo *GitRepo, fileToCompare string, filesToIgnore []string) ([]string, error) {
	var changedFiles []string
	var err error

	if fileToCompare != "" {
		changedFiles = []string{fileToCompare}
	} else {
		changedFiles, err = repo.getChangedFiles(utils.OsFileReader{})
		if err != nil {
			return nil, err
		}
	}

	filteredChangedFiles := slices.DeleteFunc(changedFiles, func(file string) bool {
		return slices.Contains(filesToIgnore, file)
	})

	return filteredChangedFiles, nil
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
