package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/comment"
	"github.com/shini4i/argo-compare/internal/comment/gitlab"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/spf13/afero"
)

const repoCredsPrefix = "REPO_CREDS_" // #nosec G101

// Dependencies aggregates runtime collaborators required by App.
type Dependencies struct {
	FS                   afero.Fs
	CmdRunner            ports.CmdRunner
	FileReader           ports.FileReader
	HelmProcessor        ports.HelmChartsProcessor
	Globber              ports.Globber
	Logger               *logging.Logger
	CommentPosterFactory CommentPosterFactory
}

// App orchestrates the end-to-end comparison workflow.
type App struct {
	cfg             Config
	fs              afero.Fs
	cmdRunner       ports.CmdRunner
	fileReader      ports.FileReader
	helmProcessor   ports.HelmChartsProcessor
	globber         ports.Globber
	logger          *logging.Logger
	repoCredentials []models.RepoCredentials
	commentFactory  CommentPosterFactory
}

// CommentPosterFactory builds a comment poster based on the active configuration.
type CommentPosterFactory func(cfg Config) (comment.Poster, error)

// New constructs an App using the supplied configuration and dependencies.
func New(cfg Config, deps Dependencies) (*App, error) {
	if cfg.CacheDir == "" {
		return nil, errors.New("cache directory must be provided")
	}

	if deps.FS == nil {
		deps.FS = afero.NewOsFs()
	}
	if deps.CmdRunner == nil {
		deps.CmdRunner = &utils.RealCmdRunner{}
	}
	if deps.FileReader == nil {
		deps.FileReader = utils.OsFileReader{}
	}
	if deps.HelmProcessor == nil {
		deps.HelmProcessor = utils.RealHelmChartProcessor{Log: deps.Logger}
	}
	if deps.Globber == nil {
		deps.Globber = utils.CustomGlobber{}
	}
	if deps.Logger == nil {
		return nil, errors.New("logger must be provided")
	}
	if deps.CommentPosterFactory == nil {
		deps.CommentPosterFactory = defaultCommentPosterFactory
	}

	return &App{
		cfg:            cfg,
		fs:             deps.FS,
		cmdRunner:      deps.CmdRunner,
		fileReader:     deps.FileReader,
		helmProcessor:  deps.HelmProcessor,
		globber:        deps.Globber,
		logger:         deps.Logger,
		commentFactory: deps.CommentPosterFactory,
	}, nil
}

// Run executes the comparison workflow and returns any terminal error.
func (a *App) Run() error {
	if err := a.collectRepoCredentials(); err != nil {
		return err
	}

	repo, err := NewGitRepo(a.fs, a.cmdRunner, a.fileReader, a.logger)
	if err != nil {
		return err
	}

	a.logger.Infof("===> Running Argo Compare version [%s]", cyan(a.cfg.Version))

	var (
		changedFiles []string
		invalidFiles []string
	)

	if a.cfg.FileToCompare != "" {
		changedFiles = filterIgnored([]string{a.cfg.FileToCompare}, a.cfg.FilesToIgnore)
		if len(changedFiles) == 0 {
			a.logger.Infof("Specified file [%s] ignored by filters. Exiting...", a.cfg.FileToCompare)
			return nil
		}
	} else {
		var result ChangedFilesResult
		result, err = repo.GetChangedFiles(a.cfg.TargetBranch, a.cfg.FilesToIgnore)
		if err != nil {
			return err
		}
		changedFiles = result.Applications
		invalidFiles = result.Invalid
	}

	if len(changedFiles) == 0 {
		a.logger.Info("No changed Application files found. Exiting...")
		return nil
	}

	if err := a.compareFiles(repo, changedFiles); err != nil {
		return err
	}

	return a.reportInvalidFiles(invalidFiles)
}

// compareFiles renders and evaluates each changed Application manifest against the target branch.
func (a *App) compareFiles(repo *GitRepo, changedFiles []string) error {
	for _, file := range changedFiles {
		if err := a.processChangedFile(repo, file); err != nil {
			return err
		}
	}
	return nil
}

type destinationAction int

const (
	destinationSkip destinationAction = iota
	destinationNone
	destinationProcess
)

// processChangedFile orchestrates comparison for a single manifest, optionally skipping targets.
func (a *App) processChangedFile(repo *GitRepo, file string) (err error) {
	a.logger.Infof("===> Processing changed application: [%s]", cyan(file))

	tmpDir, err := afero.TempDir(a.fs, a.cfg.TempDirBase, "argo-compare-")
	if err != nil {
		return err
	}

	defer func() {
		if removeErr := (afero.Afero{Fs: a.fs}).RemoveAll(tmpDir); err == nil && removeErr != nil {
			err = removeErr
		}
	}()

	if err = a.processFile(file, "src", models.Application{}, tmpDir); err != nil {
		return err
	}

	targetApp, action, err := a.resolveTargetApplication(repo, file)
	if err != nil {
		return err
	}

	if action == destinationSkip {
		return nil
	}

	if action == destinationProcess {
		if destErr := a.processFile(file, "dst", targetApp, tmpDir); destErr != nil && !a.cfg.PrintAddedManifests {
			return destErr
		}
	}

	return a.runComparison(tmpDir)
}

// resolveTargetApplication retrieves the target branch manifest and determines follow-up actions.
func (a *App) resolveTargetApplication(repo *GitRepo, file string) (models.Application, destinationAction, error) {
	app, err := repo.GetChangedFileContent(a.cfg.TargetBranch, file, a.cfg.PrintAddedManifests)

	switch {
	case errors.Is(err, gitFileDoesNotExist) && !a.cfg.PrintAddedManifests:
		return models.Application{}, destinationSkip, nil
	case errors.Is(err, models.EmptyFileError):
		return models.Application{}, destinationNone, nil
	case err != nil:
		a.logger.Errorf("Could not get the target Application from branch [%s]: %s", a.cfg.TargetBranch, err)
		return app, destinationProcess, nil
	default:
		return app, destinationProcess, nil
	}
}

// processFile prepares Helm inputs for a single manifest and renders its templates.
func (a *App) processFile(fileName string, fileType string, application models.Application, tmpDir string) error {
	target := Target{
		CmdRunner:       a.cmdRunner,
		FileReader:      a.fileReader,
		HelmProcessor:   a.helmProcessor,
		CacheDir:        a.cfg.CacheDir,
		TmpDir:          tmpDir,
		RepoCredentials: a.repoCredentials,
		Log:             a.logger,
		File:            fileName,
		Type:            fileType,
		App:             application,
	}

	if fileType == "src" {
		if err := target.parse(); err != nil {
			return err
		}
	}

	if err := target.generateValuesFiles(); err != nil {
		return err
	}

	if err := target.ensureHelmCharts(); err != nil {
		return err
	}

	if err := target.extractCharts(); err != nil {
		return err
	}

	return target.renderAppSources()
}

// runComparison executes the diff strategy for the prepared temporary workspace.
func (a *App) runComparison(tmpDir string) error {
	comparer := Compare{
		Globber:            a.globber,
		TmpDir:             tmpDir,
		PreserveHelmLabels: a.cfg.PreserveHelmLabels,
	}

	result, err := comparer.Execute()
	if err != nil {
		return err
	}

	strategies, err := a.selectDiffStrategies()
	if err != nil {
		return err
	}

	for _, strategy := range strategies {
		if err := strategy.Present(result); err != nil {
			return err
		}
	}

	return nil
}

// selectDiffStrategies picks the appropriate diff presentation implementations based on configuration.
func (a *App) selectDiffStrategies() ([]DiffStrategy, error) {
	var strategies []DiffStrategy

	if a.cfg.ExternalDiffTool != "" {
		strategies = append(strategies, ExternalDiffStrategy{
			Log:         a.logger,
			Tool:        a.cfg.ExternalDiffTool,
			ShowAdded:   a.cfg.PrintAddedManifests,
			ShowRemoved: a.cfg.PrintRemovedManifests,
		})
	} else {
		strategies = append(strategies, StdoutStrategy{
			Log:         a.logger,
			ShowAdded:   a.cfg.PrintAddedManifests,
			ShowRemoved: a.cfg.PrintRemovedManifests,
		})
	}

	if a.cfg.Comment != nil && a.cfg.Comment.Provider != CommentProviderNone {
		poster, err := a.commentFactory(a.cfg)
		if err != nil {
			return nil, err
		}
		if poster == nil {
			return nil, errors.New("comment poster factory returned nil")
		}

		strategies = append(strategies, CommentStrategy{
			Log:         a.logger,
			Poster:      poster,
			ShowAdded:   a.cfg.PrintAddedManifests,
			ShowRemoved: a.cfg.PrintRemovedManifests,
		})
	}

	return strategies, nil
}

// collectRepoCredentials loads repository credentials from environment variables.
func (a *App) collectRepoCredentials() error {
	a.logger.Debug("===> Collecting repo credentials")

	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, repoCredsPrefix) {
			continue
		}

		var repoCreds models.RepoCredentials
		if err := json.Unmarshal([]byte(strings.SplitN(env, "=", 2)[1]), &repoCreds); err != nil {
			return err
		}
		a.repoCredentials = append(a.repoCredentials, repoCreds)
	}

	for _, repo := range a.repoCredentials {
		a.logger.Debugf("▶ Found repo credentials for [%s]", cyan(repo.Url))
	}

	return nil
}

// reportInvalidFiles logs invalid manifests and returns an error when any are encountered.
func (a *App) reportInvalidFiles(invalid []string) error {
	if len(invalid) == 0 {
		return nil
	}

	a.logger.Info("===> The following yaml files are invalid and were skipped")
	for _, file := range invalid {
		a.logger.Warningf("▶ %s", file)
	}

	return errors.New("invalid files found")
}

func defaultCommentPosterFactory(cfg Config) (comment.Poster, error) {
	if cfg.Comment == nil || cfg.Comment.Provider == CommentProviderNone {
		return nil, fmt.Errorf("comment factory requested with no comment provider configured")
	}

	switch cfg.Comment.Provider {
	case CommentProviderGitLab:
		return gitlab.NewPoster(gitlab.Config{
			BaseURL:         cfg.Comment.GitLab.BaseURL,
			Token:           cfg.Comment.GitLab.Token,
			ProjectID:       cfg.Comment.GitLab.ProjectID,
			MergeRequestIID: cfg.Comment.GitLab.MergeRequestIID,
		})
	default:
		return nil, fmt.Errorf("unsupported comment provider %q", cfg.Comment.Provider)
	}
}
