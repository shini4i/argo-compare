// Package app implements the core application logic for comparing ArgoCD
// Application manifests between git branches and presenting the differences.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/comment"
	"github.com/shini4i/argo-compare/internal/comment/gitlab"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/shini4i/argo-compare/internal/sanitizer"
	"github.com/shini4i/argo-compare/internal/ui"
	"github.com/spf13/afero"
)

const repoCredsPrefix = "REPO_CREDS_" // #nosec G101

// ErrManifestValidationFailed indicates that at least one rendered manifest failed schema
// validation (or the validator itself failed to run when validation was enabled).
// The comparison still ran to completion; this error is returned at the end of Run so
// callers/CI can fail the job after the diff and any comments have been emitted.
var ErrManifestValidationFailed = errors.New("manifest validation failed")

// Dependencies aggregates runtime collaborators required by App.
type Dependencies struct {
	FS                   afero.Fs
	CmdRunner            ports.CmdRunner
	FileReader           ports.FileReader
	HelmProcessor        ports.HelmChartsProcessor
	Globber              ports.Globber
	Logger               *logger.Logger
	CommentPosterFactory CommentPosterFactory
	SensitiveDataMasker  ports.SensitiveDataMasker  // Responsible for redacting sensitive manifest fields.
	CredentialProviders  []ports.CredentialProvider // Dynamic credential providers (e.g. ECR). Optional; defaults include ECR.
	ManifestValidator    ports.ManifestValidator    // Validator for rendered manifests. Optional; defaults to KubeconformValidator if validation is enabled.
	ApplicationFetcher   ports.ApplicationFetcher   // Resolves anchored Applications. Optional; defaults to RealApplicationFetcher.
}

// App orchestrates the end-to-end comparison workflow.
type App struct {
	cfg                 Config
	fs                  afero.Fs
	cmdRunner           ports.CmdRunner
	fileReader          ports.FileReader
	helmProcessor       ports.HelmChartsProcessor
	globber             ports.Globber
	logger              *logger.Logger
	repoCredentials     []models.RepoCredentials
	credentialProviders []ports.CredentialProvider // Base providers (e.g. ECR) set at construction time.
	activeProviders     []ports.CredentialProvider // Run-scoped chain: base providers + static fallback.
	commentFactory      CommentPosterFactory
	sensitiveDataMasker ports.SensitiveDataMasker // Applied to manifest content prior to diff generation.
	validator           ports.ManifestValidator   // Optional validator for rendered manifests.
	fetcher             ports.ApplicationFetcher  // Resolves anchored Applications. Optional; defaults to a real impl.
}

// CommentPosterFactory builds a comment poster based on the active configuration.
type CommentPosterFactory func(cfg Config) (comment.Poster, error)

// New constructs an App using the supplied configuration and dependencies.
// The provided Config must include a non-empty CacheDir and Dependencies must
// include a Logger. Any nil dependency fields are replaced with sensible
// defaults (OS filesystem, real command runner, OS file reader, real Helm
// processor, globber, default comment poster factory, and a Kubernetes secret
// sensitive-data masker). It returns the constructed *App or an error if
// validation fails.
func New(cfg Config, deps Dependencies) (*App, error) {
	if cfg.CacheDir == "" {
		return nil, errors.New("cache directory must be provided")
	}

	// Default the anchor file name when callers use a Config struct literal
	// directly (NewConfig already sets it). Users who actively want to disable
	// anchor discovery should simply not commit any anchor files; setting the
	// name to an unused string is supported but explicit "disable" is out of
	// scope for v1.
	if cfg.AnchorFileName == "" {
		cfg.AnchorFileName = DefaultAnchorFileName
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
	if deps.SensitiveDataMasker == nil {
		deps.SensitiveDataMasker = sanitizer.NewKubernetesSecretMasker()
	}
	if deps.CredentialProviders == nil {
		deps.CredentialProviders = []ports.CredentialProvider{
			utils.NewECRCredentialProvider(deps.Logger),
		}
	}

	var validator ports.ManifestValidator
	if deps.ManifestValidator != nil {
		validator = deps.ManifestValidator
	} else if cfg.ValidateManifests {
		kubeconformPath := cfg.KubeconformPath
		if kubeconformPath == "" {
			kubeconformPath = "kubeconform"
		}
		validator = &KubeconformValidator{
			CmdRunner:       deps.CmdRunner,
			Path:            kubeconformPath,
			SkipKinds:       cfg.ValidateSkipKinds,
			SchemaLocations: cfg.ValidateSchemaLocations,
		}
	}

	return &App{
		cfg:                 cfg,
		fs:                  deps.FS,
		cmdRunner:           deps.CmdRunner,
		fileReader:          deps.FileReader,
		helmProcessor:       deps.HelmProcessor,
		globber:             deps.Globber,
		logger:              deps.Logger,
		credentialProviders: deps.CredentialProviders,
		commentFactory:      deps.CommentPosterFactory,
		sensitiveDataMasker: deps.SensitiveDataMasker,
		validator:           validator,
		fetcher:             deps.ApplicationFetcher,
	}, nil
}

// Run executes the comparison workflow and returns any terminal error.
// The context can be used for cancellation and timeout control.
func (a *App) Run(ctx context.Context) error {
	if err := a.collectRepoCredentials(); err != nil {
		return err
	}

	// Build the final provider chain: dynamic providers + static fallback.
	// Use a local slice to avoid mutating a.credentialProviders on repeated calls.
	providers := make([]ports.CredentialProvider, len(a.credentialProviders))
	copy(providers, a.credentialProviders)
	providers = append(providers, utils.NewStaticCredentialProvider(a.repoCredentials))
	a.activeProviders = providers

	repo, err := NewGitRepo(a.fs, a.cmdRunner, a.fileReader, a.logger)
	if err != nil {
		return err
	}

	a.logger.Infof("===> Running Argo Compare version [%s]", ui.Cyan(a.cfg.Version))

	var (
		changedFiles []string
		invalidFiles []string
		anchorGroups []AnchorGroup
	)

	if a.cfg.FileToCompare != "" {
		changedFiles = filterIgnored([]string{a.cfg.FileToCompare}, a.cfg.FilesToIgnore)
		if len(changedFiles) == 0 {
			a.logger.Infof("Specified file [%s] ignored by filters. Exiting...", a.cfg.FileToCompare)
			return nil
		}
	} else {
		var result ChangedFilesResult
		result, err = repo.GetChangedFiles(a.cfg.TargetBranch, a.cfg.FilesToIgnore, a.cfg.AnchorFileName)
		if err != nil {
			return err
		}
		changedFiles = result.Applications
		invalidFiles = result.Invalid
		anchorGroups = dedupAnchorGroups(result.AnchorGroups, changedFiles)
	}

	if len(changedFiles) == 0 && len(anchorGroups) == 0 {
		a.logger.Info("No changed Application files found. Exiting...")
		return nil
	}

	validationFailed := false
	if len(changedFiles) > 0 {
		failed, cmpErr := a.compareFiles(ctx, repo, changedFiles)
		if cmpErr != nil {
			return cmpErr
		}
		validationFailed = validationFailed || failed
	}

	if len(anchorGroups) > 0 {
		failed, anchorErr := a.compareAnchorGroups(ctx, repo, anchorGroups)
		if anchorErr != nil {
			return anchorErr
		}
		validationFailed = validationFailed || failed
	}

	if err := a.reportInvalidFiles(invalidFiles); err != nil {
		return err
	}

	if validationFailed {
		return ErrManifestValidationFailed
	}

	return nil
}

// dedupAnchorGroups drops anchor groups whose target Application file already
// appears among the changed Application files — that path is handled by the
// existing flow, so processing the anchor on top would render the same
// Application twice. Cross-repo anchors are never deduplicated since their
// target Application lives outside the local diff.
func dedupAnchorGroups(groups []AnchorGroup, changedApps []string) []AnchorGroup {
	if len(groups) == 0 || len(changedApps) == 0 {
		return groups
	}
	changed := make(map[string]struct{}, len(changedApps))
	for _, f := range changedApps {
		changed[f] = struct{}{}
	}
	out := groups[:0]
	for _, g := range groups {
		if g.Anchor.Application.Repo == "" {
			if _, dup := changed[g.Anchor.Application.Path]; dup {
				continue
			}
		}
		out = append(out, g)
	}
	return out
}

// compareFiles renders and evaluates each changed Application manifest against the target branch.
// Returns true if any application produced a non-Valid validation result (schema failure or
// validator invocation error). The bool is independent of err so the caller can complete the
// run (post comments, etc.) before deciding to exit non-zero.
func (a *App) compareFiles(ctx context.Context, repo *GitRepo, changedFiles []string) (bool, error) {
	anyFailed := false
	for _, file := range changedFiles {
		failed, err := a.processChangedFile(ctx, repo, file)
		if err != nil {
			return anyFailed, err
		}
		if failed {
			anyFailed = true
		}
	}
	return anyFailed, nil
}

type destinationAction int

const (
	destinationSkip destinationAction = iota
	destinationNone
	destinationProcess
)

// processChangedFile orchestrates comparison for a single manifest, optionally skipping targets.
// Returns a flag indicating whether any validation result for this application was non-Valid.
func (a *App) processChangedFile(ctx context.Context, repo *GitRepo, file string) (validationFailed bool, err error) {
	a.logger.Infof("===> Processing changed application: [%s]", ui.Cyan(file))

	tmpDir, err := afero.TempDir(a.fs, a.cfg.TempDirBase, "argo-compare-")
	if err != nil {
		return false, err
	}

	defer func() {
		if removeErr := (afero.Afero{Fs: a.fs}).RemoveAll(tmpDir); err == nil && removeErr != nil {
			err = removeErr
		}
	}()

	// Scoped per-comparison: keeps state local and avoids cross-app leakage.
	validationResults := make(map[string]ports.ValidationResult)

	if err = a.processFile(ctx, file, TargetTypeSource, models.Application{}, tmpDir, validationResults); err != nil {
		return false, err
	}

	targetApp, action, err := a.resolveTargetApplication(repo, file)
	if err != nil {
		return false, err
	}

	if action == destinationSkip {
		return false, nil
	}

	if action == destinationProcess {
		if destErr := a.processFile(ctx, file, TargetTypeDestination, targetApp, tmpDir, validationResults); destErr != nil && !a.cfg.PrintAddedManifests {
			return false, destErr
		}
	}

	if err := a.runComparison(ctx, tmpDir, file, validationResults); err != nil {
		return false, err
	}

	for _, r := range validationResults {
		if !r.Valid {
			validationFailed = true
			break
		}
	}
	return validationFailed, nil
}

// resolveTargetApplication retrieves the target branch manifest and determines follow-up actions.
// Unknown errors are propagated so the user sees the real failure (e.g. a git plumbing issue)
// instead of a cascading Helm error caused by processing with an empty Application.
func (a *App) resolveTargetApplication(repo *GitRepo, file string) (models.Application, destinationAction, error) {
	app, err := repo.GetChangedFileContent(a.cfg.TargetBranch, file, a.cfg.PrintAddedManifests)

	action, decideErr := decideDestinationAction(err, a.cfg.PrintAddedManifests)
	if decideErr != nil {
		return models.Application{}, 0, fmt.Errorf("get target Application from branch %q: %w", a.cfg.TargetBranch, decideErr)
	}

	if action == destinationProcess {
		return app, action, nil
	}
	return models.Application{}, action, nil
}

// decideDestinationAction maps the outcome of GetChangedFileContent to a destinationAction.
// Errors other than the two named sentinels are returned to the caller; previously they
// were logged and silently downgraded to destinationProcess, which produced confusing
// downstream Helm failures instead of surfacing the real cause.
func decideDestinationAction(err error, printAdded bool) (destinationAction, error) {
	switch {
	case errors.Is(err, errGitFileDoesNotExist) && !printAdded:
		return destinationSkip, nil
	case errors.Is(err, models.ErrEmptyFile):
		return destinationNone, nil
	case err != nil:
		return 0, err
	default:
		return destinationProcess, nil
	}
}

// processFile prepares Helm inputs for a single manifest and renders its templates.
// validationResults is populated when a validator is configured; entries are keyed by fileType.
func (a *App) processFile(ctx context.Context, fileName, fileType string, application models.Application, tmpDir string, validationResults map[string]ports.ValidationResult) error {
	target := Target{
		CmdRunner:           a.cmdRunner,
		FileReader:          a.fileReader,
		HelmProcessor:       a.helmProcessor,
		Globber:             a.globber,
		CacheDir:            a.cfg.CacheDir,
		TmpDir:              tmpDir,
		CredentialProviders: a.activeProviders,
		Log:                 a.logger,
		File:                fileName,
		Type:                fileType,
		App:                 application,
	}

	if fileType == TargetTypeSource {
		if err := target.parse(); err != nil {
			return err
		}
	}

	if err := target.generateValuesFiles(); err != nil {
		return err
	}

	if err := target.ensureHelmCharts(ctx); err != nil {
		return err
	}

	if err := target.extractCharts(ctx); err != nil {
		return err
	}

	if err := target.renderAppSources(ctx); err != nil {
		return err
	}

	// Only validate source manifests: src represents the post-merge state (what will land
	// on the target branch). Validating dst (current target branch state) would surface
	// pre-existing breakage unrelated to the PR, which is noise for a merge gate.
	if a.validator != nil && fileType == TargetTypeSource {
		// Rendered manifests land at <tmpDir>/templates/<src|dst> (set by RenderAppSource).
		manifests := filepath.Join(tmpDir, "templates", fileType)
		result, err := a.validator.Validate(ctx, fileType, manifests)
		if err != nil {
			a.logger.Warningf("Manifest validation failed: %v", err)
			// Record a synthetic result so the failure surfaces in presenters.
			validationResults[fileType] = ports.ValidationResult{
				Target:          fileType,
				InvocationError: err.Error(),
			}
		} else {
			validationResults[fileType] = result
			if !result.Valid {
				a.logger.Warningf("Validation errors found: %d issues", result.ErrorCount)
			}
		}
	}

	return nil
}

// runComparison executes the diff strategy for the prepared temporary workspace.
func (a *App) runComparison(ctx context.Context, tmpDir, applicationFile string, validationResults map[string]ports.ValidationResult) error {
	comparer := Compare{
		Fs:                 a.fs,
		Globber:            a.globber,
		TmpDir:             tmpDir,
		PreserveHelmLabels: a.cfg.PreserveHelmLabels,
		Masker:             a.sensitiveDataMasker,
	}

	result, err := comparer.Execute()
	if err != nil {
		return err
	}

	if len(validationResults) > 0 {
		result.ValidationResults = validationResults
	}

	strategies, err := a.selectDiffStrategies(applicationFile)
	if err != nil {
		return err
	}

	for _, strategy := range strategies {
		if err := strategy.Present(ctx, result); err != nil {
			return err
		}
	}

	return nil
}

// selectDiffStrategies picks the appropriate diff presentation implementations based on configuration.
func (a *App) selectDiffStrategies(applicationFile string) ([]DiffPresenter, error) {
	var strategies []DiffPresenter

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
			return nil, fmt.Errorf("comment poster factory returned nil for provider %q", a.cfg.Comment.Provider)
		}

		strategies = append(strategies, CommentStrategy{
			Log:             a.logger,
			Poster:          poster,
			ShowAdded:       a.cfg.PrintAddedManifests,
			ShowRemoved:     a.cfg.PrintRemovedManifests,
			ApplicationPath: applicationFile,
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
		a.logger.Debugf("▶ Found repo credentials for [%s]", ui.Cyan(repo.Url))
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

// defaultCommentPosterFactory returns a poster instance for the configured comment provider.
// It expects cfg.Comment to be non-nil and already validated by the caller.
func defaultCommentPosterFactory(cfg Config) (comment.Poster, error) {
	if cfg.Comment == nil {
		return nil, fmt.Errorf("comment factory requested with nil comment configuration")
	}
	if cfg.Comment.Provider == CommentProviderNone {
		return nil, fmt.Errorf("comment factory requested with comment provider %q", CommentProviderNone)
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
