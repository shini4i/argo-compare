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
	"github.com/shini4i/argo-compare/internal/anchor"
	"github.com/shini4i/argo-compare/internal/comment"
	"github.com/shini4i/argo-compare/internal/comment/gitlab"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/shini4i/argo-compare/internal/sanitizer"
	"github.com/shini4i/argo-compare/internal/ui"
	"github.com/spf13/afero"
)

const repoCredsPrefix = "REPO_CREDS_" // #nosec G101

// defaultKubeconformBinary is the executable name used when no explicit
// KubeconformPath is configured; resolved from PATH.
const defaultKubeconformBinary = "kubeconform"

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
	commentPoster       comment.Poster            // Built on first use and kept; nil until a comparison needs it.
	commentSections     []commentSection          // One per comparison, published together at the end of the run.
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

	// NewConfig defaults AnchorFileName to DefaultAnchorFileName for the
	// public-API path. Struct-literal Config users get whatever they set,
	// including the empty-string opt-out documented on WithAnchorFileName
	// and surfaced via --anchor-file / ARGO_COMPARE_ANCHOR_FILE.

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
			kubeconformPath = defaultKubeconformBinary
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

	inputs, err := a.collectComparisonInputs(repo)
	if err != nil {
		return err
	}
	if inputs.exitEarly {
		return nil
	}

	if len(inputs.changed) == 0 && len(inputs.groups) == 0 {
		a.logger.Info("No changed Application files found. Exiting...")
		return nil
	}

	validationFailed, compareErr := a.runComparisons(ctx, repo, inputs.changed, inputs.groups)

	// Publish even when a later comparison failed: what was already compared is
	// still what the reviewer needs. A cancelled context is the exception — the
	// poster honours it, so an interrupted run publishes nothing.
	if flushErr := a.flushComments(ctx); flushErr != nil {
		if compareErr == nil {
			return flushErr
		}
		a.logger.Errorf("Failed to publish the comparison comment: %s", flushErr)
	}
	if compareErr != nil {
		return compareErr
	}

	if err := a.reportInvalidFiles(inputs.invalid); err != nil {
		return err
	}

	if validationFailed {
		return ErrManifestValidationFailed
	}

	return nil
}

// comparisonInputs bundles the inputs needed by the comparison loop. exitEarly
// is set when the only configured target was filtered out, allowing the caller
// to short-circuit without surfacing an error.
type comparisonInputs struct {
	changed   []string
	invalid   []string
	groups    []AnchorGroup
	exitEarly bool
}

// collectComparisonInputs resolves the changed Application files, invalid
// manifests, and anchor groups that should be processed in this run. The
// explicit FileToCompare path and the diff-based path are handled here so Run
// stays a flat orchestration step.
func (a *App) collectComparisonInputs(repo *GitRepo) (comparisonInputs, error) {
	if a.cfg.FileToCompare != "" {
		changed := filterIgnored([]string{a.cfg.FileToCompare}, a.cfg.FilesToIgnore)
		if len(changed) == 0 {
			a.logger.Infof("Specified file [%s] ignored by filters. Exiting...", a.cfg.FileToCompare)
			return comparisonInputs{exitEarly: true}, nil
		}
		return comparisonInputs{changed: changed}, nil
	}

	result, err := repo.GetChangedFiles(a.cfg.TargetBranch, a.cfg.FilesToIgnore, a.cfg.AnchorFileName)
	if err != nil {
		return comparisonInputs{}, err
	}
	return comparisonInputs{
		changed: result.Applications,
		invalid: result.Invalid,
		groups:  dedupAnchorGroups(result.AnchorGroups, result.Applications),
	}, nil
}

// runComparisons fans out the comparison work across changed Application files
// and anchor groups, returning whether any validation step produced a non-Valid
// result. Errors short-circuit; the validation flag is accumulated across both
// branches so a single failure surfaces ErrManifestValidationFailed.
func (a *App) runComparisons(ctx context.Context, repo *GitRepo, changedFiles []string, anchorGroups []AnchorGroup) (bool, error) {
	validationFailed := false

	if len(changedFiles) > 0 {
		failed, err := a.compareFiles(ctx, repo, changedFiles)
		if err != nil {
			return false, err
		}
		validationFailed = validationFailed || failed
	}

	if len(anchorGroups) > 0 {
		failed, err := a.compareAnchorGroups(ctx, repo, anchorGroups)
		if err != nil {
			return false, err
		}
		validationFailed = validationFailed || failed
	}

	return validationFailed, nil
}

// dedupAnchorGroups drops anchor groups whose target Application file already
// appears among the changed Application files — that path is handled by the
// existing flow, so processing the anchor on top would render the same
// Application twice. Cross-repo anchors are never deduplicated since their
// target Application lives outside the local diff.
//
// Paths on both sides are normalized via filepath.Clean so that anchor entries
// spelled "./apps/foo.yaml" still match a changedApps entry of "apps/foo.yaml".
func dedupAnchorGroups(groups []AnchorGroup, changedApps []string) []AnchorGroup {
	if len(groups) == 0 {
		return groups
	}
	changed := make(map[string]struct{}, len(changedApps))
	for _, f := range changedApps {
		changed[filepath.Clean(f)] = struct{}{}
	}

	seen := make(map[anchor.ApplicationRef]struct{}, len(groups))
	out := groups[:0]
	for _, g := range groups {
		ref := g.Anchor.Application
		ref.Path = filepath.Clean(ref.Path)

		if ref.Repo == "" {
			if _, dup := changed[ref.Path]; dup {
				continue
			}
		}
		// Two chart directories anchored to one manifest describe a single
		// comparison. Rendering it per group would repeat the whole diff — and
		// for an ApplicationSet, repeat it once per generated Application. The
		// repo is keyed by identity, so two spellings of one remote still match.
		ref.Repo = normalizeRepoIdentity(ref.Repo)
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}

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
	appSet, err := a.applicationSetFor(file)
	if err != nil {
		return false, err
	}
	if appSet != nil {
		return a.processChangedApplicationSet(ctx, repo, file, appSet)
	}

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

	if err = a.processFile(ctx, repo, file, TargetTypeSource, models.Application{}, tmpDir, validationResults); err != nil {
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
		if destErr := a.processFile(ctx, repo, file, TargetTypeDestination, targetApp, tmpDir, validationResults); destErr != nil && !a.cfg.PrintAddedManifests {
			return false, destErr
		}
	}

	if err := a.runComparison(ctx, tmpDir, file, validationResults); err != nil {
		return false, err
	}

	return anyValidationFailed(validationResults), nil
}

// applicationSetFor returns the ApplicationSet held in file, or nil when the
// manifest is a plain Application that the single-Application flow handles.
func (a *App) applicationSetFor(file string) (*models.ApplicationSet, error) {
	appSet, err := parseApplicationSet(a.fileReader, file)
	switch {
	case err == nil:
		return appSet, nil
	case errors.Is(err, models.ErrNotApplicationSet):
		return nil, nil
	default:
		return nil, err
	}
}

// anyValidationFailed reports whether any recorded leg failed schema validation.
func anyValidationFailed(results map[string]ports.ValidationResult) bool {
	for _, result := range results {
		if !result.Valid {
			return true
		}
	}
	return false
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

// prepareChartFromPath materializes a path-based source's chart directory into
// the layout the renderer expects. The source leg copies from the local
// working tree; the destination leg extracts from the merge-base tree of the
// configured target branch. After materialization, subchart dependencies
// declared in Chart.yaml are resolved into chart/charts/ via
// `helm dependency build`.
func (a *App) prepareChartFromPath(ctx context.Context, repo *GitRepo, target *Target, fileType string) error {
	switch fileType {
	case TargetTypeSource:
		repoRoot, err := GetGitRepoRoot()
		if err != nil {
			return fmt.Errorf("resolve repo root for path-based source: %w", err)
		}
		if err := target.MaterializeChartFromWorkingTree(ctx, a.fs, repoRoot); err != nil {
			return err
		}
	case TargetTypeDestination:
		tree, err := repo.MergeBaseTreeFor(a.cfg.TargetBranch)
		if err != nil {
			return err
		}
		if err := target.MaterializeChartFromTree(ctx, a.fs, tree); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown render leg %q", fileType)
	}
	return target.BuildChartDependencies(ctx)
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
//
// For registry-based sources (spec.source.chart set) the chart is fetched via
// the existing helm pull + extract pipeline. For path-based sources
// (spec.source.path set) the chart directory is materialized into the same
// on-disk layout from the local working tree (src leg) or the merge-base tree
// (dst leg), and the registry plumbing is skipped.
func (a *App) processFile(ctx context.Context, repo *GitRepo, fileName, fileType string, application models.Application, tmpDir string, validationResults map[string]ports.ValidationResult) error {
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

	return a.renderTarget(ctx, repo, &target, fileType, validationResults)
}

// renderTarget drives the shared Helm pipeline for a target whose Application
// is already resolved: classify sources, materialize the chart, render, and
// validate the result.
func (a *App) renderTarget(ctx context.Context, repo *GitRepo, target *Target, fileType string, validationResults map[string]ports.ValidationResult) error {
	if err := target.ClassifySources(); err != nil {
		return err
	}

	if err := target.generateValuesFiles(); err != nil {
		return err
	}

	if err := a.prepareChart(ctx, repo, target, fileType); err != nil {
		return err
	}

	if err := target.renderAppSources(ctx); err != nil {
		return err
	}

	a.runManifestValidation(ctx, fileType, target.TmpDir, validationResults)
	return nil
}

// prepareChart materializes the chart inputs for a target. Path-based sources
// are copied from the working tree (src) or extracted from the merge-base tree
// (dst); registry-based sources go through the existing helm pull + extract
// pipeline.
func (a *App) prepareChart(ctx context.Context, repo *GitRepo, target *Target, fileType string) error {
	if target.PathBased() {
		return a.prepareChartFromPath(ctx, repo, target, fileType)
	}
	if err := target.ensureHelmCharts(ctx); err != nil {
		return err
	}
	return target.extractCharts(ctx)
}

// runManifestValidation invokes the configured validator on rendered source
// manifests and records the outcome. Destination manifests are intentionally
// skipped: src reflects the post-merge state, while dst reflects the current
// target branch — validating dst would surface pre-existing breakage unrelated
// to the PR, which is noise for a merge gate.
func (a *App) runManifestValidation(ctx context.Context, fileType, tmpDir string, validationResults map[string]ports.ValidationResult) {
	if a.validator == nil || fileType != TargetTypeSource {
		return
	}
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
		return
	}
	validationResults[fileType] = result
	if !result.Valid {
		a.logger.Warningf("Validation errors found: %d issues", result.ErrorCount)
	}
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
		if _, err := a.commentPosterForRun(); err != nil {
			return nil, err
		}

		strategies = append(strategies, commentCollector{app: a, label: applicationFile})
	}

	return strategies, nil
}

// commentCollector defers one comparison's result to the run's single comment,
// so an ApplicationSet generating many Applications does not post a note each.
type commentCollector struct {
	app   *App
	label string
}

// Present adds the comparison to the run's batch rather than publishing it;
// flushComments publishes the batch once every comparison has finished.
func (c commentCollector) Present(_ context.Context, result ComparisonResult) error {
	c.app.commentSections = append(c.app.commentSections, commentSection{label: c.label, result: result})
	return nil
}

// commentPosterForRun builds the poster on first use and reuses it, so one
// client serves every comparison in the run.
func (a *App) commentPosterForRun() (comment.Poster, error) {
	if a.commentPoster != nil {
		return a.commentPoster, nil
	}

	poster, err := a.commentFactory(a.cfg)
	if err != nil {
		return nil, err
	}
	if poster == nil {
		return nil, fmt.Errorf("comment poster factory returned nil for provider %q", a.cfg.Comment.Provider)
	}

	a.commentPoster = poster

	return poster, nil
}

// flushComments publishes every comparison collected during the run as one
// comment, splitting only when the note limit demands it. The batch is cleared
// as it is taken, so a second run publishes only its own comparisons.
func (a *App) flushComments(ctx context.Context) error {
	if len(a.commentSections) == 0 || a.commentPoster == nil {
		return nil
	}

	sections := a.commentSections
	a.commentSections = nil

	return CommentStrategy{
		Log:         a.logger,
		Poster:      a.commentPoster,
		ShowAdded:   a.cfg.PrintAddedManifests,
		ShowRemoved: a.cfg.PrintRemovedManifests,
	}.PresentSections(ctx, sections)
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

	switch cfg.Comment.Provider {
	case CommentProviderGitLab:
		return gitlab.NewPoster(gitlab.Config{
			BaseURL:         cfg.Comment.GitLab.BaseURL,
			Token:           cfg.Comment.GitLab.Token,
			ProjectID:       cfg.Comment.GitLab.ProjectID,
			MergeRequestIID: cfg.Comment.GitLab.MergeRequestIID,
		})
	case CommentProviderNone:
		return nil, fmt.Errorf("comment factory requested with comment provider %q", CommentProviderNone)
	default:
		return nil, fmt.Errorf("unsupported comment provider %q", cfg.Comment.Provider)
	}
}
