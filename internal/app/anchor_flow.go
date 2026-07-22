package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/shini4i/argo-compare/internal/anchor"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/shini4i/argo-compare/internal/ui"

	"github.com/spf13/afero"
)

// ErrAnchorRepoMismatch is returned when an anchored Application's
// spec.source.repoURL does not identify the same Git repository argo-compare
// is running in. The v1 path-based renderer only supports rendering from the
// local repo; third-repo chart sources are explicitly out of scope.
var ErrAnchorRepoMismatch = errors.New("anchored Application spec.source.repoURL does not match the local repository")

// ErrAnchorNotPathBased is returned when an anchored Application uses a
// registry-based source. The anchor flow exists specifically to render a
// chart from a Git path; chart-based sources go through the existing
// changed-Application-file flow.
var ErrAnchorNotPathBased = errors.New("anchored Application is not path-based")

// ErrValueFileMissingFromSource is returned when a cross-repo anchored
// Application references a spec.source.helm.valueFiles entry that does not
// exist in the chart materialized from the current branch's working tree.
//
// This is the legible face of issue #158: the chart is read from the PR
// working tree while the Application is read from the anchored repo's branch
// tip, so a PR that restructures the chart's values files (e.g. splits one
// file into several) leaves the two out of sync until the Application is
// updated in its own repo. Without this preflight the run fails deep inside
// `helm template` with an opaque "no such file" error that gives no hint of
// the anchor/branch-tip mismatch.
var ErrValueFileMissingFromSource = errors.New("anchored Application references a values file absent from the current branch")

// compareAnchorGroups runs the path-based rendering pipeline for every anchor
// group discovered in the diff. It returns true if any rendering produced a
// non-Valid validation result; the error return is reserved for terminal
// failures that prevent rendering altogether.
func (a *App) compareAnchorGroups(ctx context.Context, repo *GitRepo, groups []AnchorGroup) (bool, error) {
	if len(groups) == 0 {
		return false, nil
	}

	repoRoot, err := GetGitRepoRoot()
	if err != nil {
		return false, fmt.Errorf("resolve repo root for anchor flow: %w", err)
	}
	originURL, err := repo.OriginURL()
	if err != nil {
		return false, err
	}

	fetcher := a.applicationFetcher()

	anyFailed := false
	for _, group := range groups {
		failed, err := a.processAnchorGroup(ctx, repo, group, fetcher, repoRoot, originURL)
		if err != nil {
			return anyFailed, err
		}
		if failed {
			anyFailed = true
		}
	}
	return anyFailed, nil
}

// processAnchorGroup renders, diffs, and validates the Application that the
// anchor points to. tmpDir is created fresh per group and cleaned up at end.
func (a *App) processAnchorGroup(ctx context.Context, repo *GitRepo, group AnchorGroup, fetcher ports.ApplicationFetcher, repoRoot, originURL string) (validationFailed bool, err error) {
	a.logger.Infof("===> Processing anchored chart in [%s]", ui.Cyan(group.Dir))

	app, err := fetcher.Fetch(ctx, group.Anchor.Application, repoRoot)
	if err != nil {
		return false, err
	}

	classifyTarget := Target{App: app}
	if classifyErr := classifyTarget.ClassifySources(); classifyErr != nil {
		return false, classifyErr
	}
	if !classifyTarget.PathBased() {
		return false, fmt.Errorf("%w: %s", ErrAnchorNotPathBased, anchorRefDisplay(group.Anchor.Application))
	}
	if mismatchErr := assertSameRepo(app.Spec.Source, app.Spec.Sources, originURL); mismatchErr != nil {
		return false, fmt.Errorf("%w: %w", ErrAnchorRepoMismatch, mismatchErr)
	}

	tmpDir, err := afero.TempDir(a.fs, a.cfg.TempDirBase, "argo-compare-anchor-")
	if err != nil {
		return false, err
	}
	defer func() {
		if removeErr := (afero.Afero{Fs: a.fs}).RemoveAll(tmpDir); err == nil && removeErr != nil {
			err = removeErr
		}
	}()

	validationResults := make(map[string]ports.ValidationResult)

	proceed, err := a.renderAnchorLegs(ctx, app, group, tmpDir, repo, repoRoot, validationResults)
	if err != nil {
		return false, err
	}
	if !proceed {
		return false, nil
	}

	if err = a.runComparison(ctx, tmpDir, group.Anchor.Application.Path, validationResults); err != nil {
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

// renderAnchorLegs renders the source and then the destination leg for an
// anchor group. It returns proceed=false when the comparison should be
// skipped: the anchored chart directory is absent from the merge-base tree
// (the Application is being added on this branch) and --print-added-manifests
// is off, so there is no baseline and nothing meaningful to show. With
// --print-added-manifests the source-only render is kept so the diff surfaces
// as all-added, mirroring the registry-chart flow's new-Application handling.
func (a *App) renderAnchorLegs(ctx context.Context, app models.Application, group AnchorGroup, tmpDir string, repo *GitRepo, repoRoot string, validationResults map[string]ports.ValidationResult) (bool, error) {
	if err := a.renderAnchorLeg(ctx, app, group.Anchor.Application, tmpDir, TargetTypeSource, repo, repoRoot, validationResults); err != nil {
		return false, err
	}

	destErr := a.renderAnchorLeg(ctx, app, group.Anchor.Application, tmpDir, TargetTypeDestination, repo, repoRoot, validationResults)
	switch {
	case destErr == nil:
		return true, nil
	case errors.Is(destErr, ErrChartPathNotInTree):
		a.logger.Warning(ui.Yellow(fmt.Sprintf(
			"The anchored chart for [%s] does not exist in target branch %s, assuming it is a new Application",
			group.Dir, a.cfg.TargetBranch)))
		return a.cfg.PrintAddedManifests, nil
	default:
		return false, destErr
	}
}

// renderAnchorLeg prepares the chart directory for one leg (src or dst) and
// drives the existing Helm values / render / validate pipeline.
func (a *App) renderAnchorLeg(ctx context.Context, app models.Application, ref anchor.ApplicationRef, tmpDir, leg string, repo *GitRepo, repoRoot string, validationResults map[string]ports.ValidationResult) error {
	target := Target{
		CmdRunner:           a.cmdRunner,
		FileReader:          a.fileReader,
		HelmProcessor:       a.helmProcessor,
		Globber:             a.globber,
		CacheDir:            a.cfg.CacheDir,
		TmpDir:              tmpDir,
		CredentialProviders: a.activeProviders,
		Log:                 a.logger,
		Type:                leg,
		App:                 app,
	}

	if err := a.materializeChartForLeg(ctx, &target, leg, repo, repoRoot); err != nil {
		return err
	}

	// Cross-repo source leg: the chart comes from the PR working tree but the
	// Application (and its valueFiles list) came from the anchored repo's branch
	// tip. Surface a mismatch here with an actionable error instead of letting
	// `helm template` fail opaquely on the missing file. Same-repo anchors read
	// both from the working tree, so they cannot drift this way.
	if leg == TargetTypeSource && ref.Repo != "" {
		if err := target.checkSourceValueFilesPresent(a.fs, ref); err != nil {
			return err
		}
	}

	if err := target.BuildChartDependencies(ctx); err != nil {
		return err
	}

	if err := target.generateValuesFiles(); err != nil {
		return err
	}
	if err := target.renderAppSources(ctx); err != nil {
		return err
	}

	if a.validator != nil && leg == TargetTypeSource {
		manifests := filepath.Join(tmpDir, "templates", leg)
		result, err := a.validator.Validate(ctx, leg, manifests)
		if err != nil {
			a.logger.Warningf("Manifest validation failed: %v", err)
			validationResults[leg] = ports.ValidationResult{Target: leg, InvocationError: err.Error()}
			return nil
		}
		validationResults[leg] = result
		if !result.Valid {
			a.logger.Warningf("Validation errors found: %d issues", result.ErrorCount)
		}
	}
	return nil
}

// materializeChartForLeg checks out the chart sources for one comparison leg
// into target's TmpDir: the working tree for the source leg, and the merge-base
// tree against the target branch for the destination leg.
func (a *App) materializeChartForLeg(ctx context.Context, target *Target, leg string, repo *GitRepo, repoRoot string) error {
	switch leg {
	case TargetTypeSource:
		return target.MaterializeChartFromWorkingTree(ctx, a.fs, repoRoot)
	case TargetTypeDestination:
		mergeBaseTree, err := repo.MergeBaseTreeFor(a.cfg.TargetBranch)
		if err != nil {
			return err
		}
		return target.MaterializeChartFromTree(ctx, a.fs, mergeBaseTree)
	default:
		return fmt.Errorf("unknown render leg %q", leg)
	}
}

// checkSourceValueFilesPresent verifies that every values file referenced by
// the anchored Application exists in the chart materialized for the source leg
// (TmpDir/charts/<Type>/<ChartName>). It is meant for the cross-repo source
// leg, where the chart is read from the PR working tree while the Application
// is read from the anchored repo's branch tip; a mismatch there yields
// ErrValueFileMissingFromSource with guidance rather than an opaque Helm error.
//
// Entries that validateValueFile would reject (empty, absolute, or "..") are
// skipped so their specific validation error is not masked by this preflight.
func (t *Target) checkSourceValueFilesPresent(fs afero.Fs, ref anchor.ApplicationRef) error {
	afs := afero.Afero{Fs: fs}
	for _, src := range t.pathSources() {
		if src == nil {
			continue
		}
		chartDir := filepath.Join(t.TmpDir, "charts", t.Type, effectiveChartName(src))
		for _, vf := range src.Helm.ValueFiles {
			if vf == "" || filepath.IsAbs(vf) || strings.HasPrefix(filepath.Clean(vf), "..") {
				continue
			}
			exists, err := afs.Exists(filepath.Join(chartDir, vf))
			if err != nil {
				return fmt.Errorf("check anchored values file %q: %w", vf, err)
			}
			if !exists {
				return fmt.Errorf(
					"%w: %q (referenced by anchored Application %s) is not present in %q on the current branch. "+
						"This usually means the pull request restructured the chart's values files, but the Application definition — read from the anchored repo's branch tip — still points at the old layout. "+
						"Update spec.source.helm.valueFiles in that Application to match, and land it (see docs/anchored-repositories.md)",
					ErrValueFileMissingFromSource, vf, anchorRefDisplay(ref), src.Path)
			}
		}
	}
	return nil
}

// applicationFetcher returns the configured fetcher or builds a default real
// implementation on demand. Tests override this via Dependencies.
func (a *App) applicationFetcher() ports.ApplicationFetcher {
	if a.fetcher != nil {
		return a.fetcher
	}
	return &RealApplicationFetcher{
		FS:          a.fs,
		FileReader:  a.fileReader,
		CmdRunner:   a.cmdRunner,
		Log:         a.logger,
		GitUsername: a.cfg.GitUsername,
		GitToken:    a.cfg.GitToken,
	}
}

// anchorRefDisplay produces a human-readable identifier for log/error messages.
func anchorRefDisplay(ref anchor.ApplicationRef) string {
	if ref.Repo == "" {
		return ref.Path + " (local)"
	}
	branch := ref.Branch
	if branch == "" {
		branch = "<remote default>"
	}
	return fmt.Sprintf("%s@%s:%s", redactRepo(ref.Repo), branch, ref.Path)
}

// assertSameRepo verifies that every path-based source's repoURL identifies
// the same repository as originURL. An empty originURL (no origin remote
// configured locally) is treated as a hard fail because the v1 anchor flow
// relies on the local repo for the chart contents.
func assertSameRepo(single *models.Source, sources []*models.Source, originURL string) error {
	if originURL == "" {
		return errors.New("local repo has no origin remote configured")
	}
	check := func(s *models.Source) error {
		if s == nil || s.Path == "" {
			return nil
		}
		if !repoIdentityMatches(s.RepoURL, originURL) {
			return fmt.Errorf("spec.source.repoURL %q does not match origin %q", redactRepo(s.RepoURL), redactRepo(originURL))
		}
		return nil
	}
	if len(sources) > 0 {
		for _, s := range sources {
			if err := check(s); err != nil {
				return err
			}
		}
		return nil
	}
	return check(single)
}

// normalizeRepoIdentity collapses common Git URL spellings (https, ssh,
// scp-style, oci-prefixed, file://) into a host/path key that is stable across
// formats. The port is deliberately dropped: ArgoCD Applications commonly use
// an explicit SSH port (e.g. ssh://git@host:1022/group/repo.git) while the
// local CI clone uses the portless HTTPS origin for the same repository, and
// these must compare equal. .git suffix and trailing slashes are stripped.
// file:// is stripped so that a bare local-path origin (e.g. /srv/git/foo.git)
// matches its file:///srv/git/foo.git equivalent.
func normalizeRepoIdentity(repoURL string) string {
	s := strings.TrimSpace(repoURL)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "oci://")
	s = strings.TrimPrefix(s, "file://")

	// scp-style: user@host:path (no scheme, no slashes before colon)
	if i := strings.Index(s, "@"); i > 0 && !strings.Contains(s[:i], "://") {
		rest := s[i+1:]
		if j := strings.Index(rest, ":"); j > 0 && !strings.Contains(rest[:j], "/") {
			return strings.ToLower(rest[:j]) + "/" + stripTrailingPathNoise(rest[j+1:])
		}
	}

	// parsed.Hostname() strips any :port (and IPv6 brackets), so URLs that
	// differ only by port normalize to the same identity.
	if parsed, err := url.Parse(s); err == nil && parsed.Hostname() != "" {
		return strings.ToLower(parsed.Hostname()) + stripTrailingPathNoise(parsed.Path)
	}

	return stripTrailingPathNoise(s)
}

// stripTrailingPathNoise removes a trailing `.git` suffix and trailing slashes.
func stripTrailingPathNoise(p string) string {
	p = strings.TrimSuffix(p, "/")
	p = strings.TrimSuffix(p, ".git")
	p = strings.TrimSuffix(p, "/")
	return p
}

// repoIdentityMatches reports whether two Git URLs identify the same repo
// under normalizeRepoIdentity. Empty URLs never match anything.
func repoIdentityMatches(a, b string) bool {
	na, nb := normalizeRepoIdentity(a), normalizeRepoIdentity(b)
	if na == "" || nb == "" {
		return false
	}
	return na == nb
}
