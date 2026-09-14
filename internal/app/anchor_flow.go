package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/shini4i/argo-compare/internal/anchor"
	"github.com/shini4i/argo-compare/internal/appset"
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

// ErrAnchorNotPathBased is returned when an anchored Application renders a
// registry chart and takes no values from this repository either. Such an
// Application cannot be affected by a change under the anchor, so it goes
// through the changed-Application-file flow instead.
var ErrAnchorNotPathBased = errors.New("anchored Application is not path-based")

// ErrValueFileMissingFromSource is returned when a cross-repo anchored Application names a
// spec.source.helm.valueFiles entry the chart in this branch's working tree does not have. The
// Application is read from its own repository's branch tip, so a PR that restructures the values
// files leaves the two out of sync until that Application is updated (issue #158).
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

// processAnchorGroup compares whatever the anchor points at: a single
// Application, or every Application an ApplicationSet generates.
func (a *App) processAnchorGroup(ctx context.Context, repo *GitRepo, group AnchorGroup, fetcher ports.ApplicationFetcher, repoRoot, originURL string) (bool, error) {
	a.logger.Infof("===> Processing anchored chart in [%s]", ui.Cyan(group.Dir))

	manifest, err := fetcher.Fetch(ctx, group.Anchor.Application, repoRoot)
	if err != nil {
		return false, err
	}

	if manifest.ApplicationSet != nil {
		return a.processAnchoredApplicationSet(ctx, repo, group, manifest, originURL)
	}
	if manifest.Application == nil {
		return false, fmt.Errorf("fetcher resolved %s to no manifest", anchorRefDisplay(group.Anchor.Application))
	}

	return a.processAnchoredApplication(ctx, repo, group, *manifest.Application, repoRoot, originURL)
}

// processAnchoredApplicationSet compares every Application an anchored
// ApplicationSet generates. The manifest is read once — an anchor exists
// because the chart changed, not the manifest — while anchoredExpansionTrees
// decides which tree each leg expands it against.
func (a *App) processAnchoredApplicationSet(ctx context.Context, repo *GitRepo, group AnchorGroup, manifest ports.AnchoredManifest, originURL string) (bool, error) {
	ref := group.Anchor.Application
	appSet := manifest.ApplicationSet

	srcTree, dstTree, err := a.anchoredExpansionTrees(repo, manifest, ref, originURL)
	if err != nil {
		return false, fmt.Errorf("anchored ApplicationSet %s: %w", anchorRefDisplay(ref), err)
	}

	srcApps, err := expandAnchoredApplicationSet(appSet, srcTree, ref, TargetTypeSource)
	if err != nil {
		return false, err
	}

	dstApps, err := expandAnchoredApplicationSet(appSet, dstTree, ref, TargetTypeDestination)
	if err != nil {
		return false, err
	}

	pairs := pairGeneratedApplications(srcApps, dstApps)
	inScope, err := a.anchoredPairsInScope(ref, pairs, originURL)
	if err != nil {
		return false, err
	}

	a.logger.Infof("Anchored ApplicationSet %s generates %d Application(s) on this branch and %d on %s; comparing %d",
		ui.Cyan(anchorRefDisplay(ref)), len(srcApps), len(dstApps), a.cfg.TargetBranch, len(inScope))

	anyFailed := false
	for _, pair := range inScope {
		failed, err := a.compareGeneratedApplication(ctx, repo, ref.Path, pair)
		if err != nil {
			return anyFailed, err
		}
		if failed {
			anyFailed = true
		}
	}

	return anyFailed, nil
}

// anchoredPairsInScope keeps the generated Applications that read chart content
// from this repository — the only ones an anchor can be reporting on. It fails
// when none qualify, because an anchor names a change that must not go
// uncompared with nothing said.
func (a *App) anchoredPairsInScope(ref anchor.ApplicationRef, pairs []generatedPair, originURL string) ([]generatedPair, error) {
	// Every path-based render reads the local repo, so without an origin to
	// compare against there is no way to tell which Applications belong to it.
	if originURL == "" {
		return nil, fmt.Errorf("anchored ApplicationSet %s: local repo has no origin remote configured", anchorRefDisplay(ref))
	}

	inScope := make([]generatedPair, 0, len(pairs))
	for _, pair := range pairs {
		scoped, keep, err := a.scopeAnchoredPair(pair, originURL)
		if err != nil {
			return nil, fmt.Errorf("anchored ApplicationSet %s: %w", anchorRefDisplay(ref), err)
		}
		if keep {
			inScope = append(inScope, scoped)
		}
	}

	if len(inScope) == 0 {
		return nil, fmt.Errorf("anchored ApplicationSet %s generates no Application rendering a chart from this repository, so the change under the anchor is not covered by any comparison",
			anchorRefDisplay(ref))
	}

	return inScope, nil
}

// scopeAnchoredPair drops the comparison legs an anchor cannot report on,
// judging each independently so an Application moving into or out of this
// repository is still compared on the side that reads it. keep is false when
// neither leg qualifies.
func (a *App) scopeAnchoredPair(pair generatedPair, originURL string) (generatedPair, bool, error) {
	srcReason, err := anchoredLegSkipReason(pair.src, originURL)
	if err != nil {
		return pair, false, err
	}
	dstReason, err := anchoredLegSkipReason(pair.dst, originURL)
	if err != nil {
		return pair, false, err
	}

	if srcReason != "" {
		pair.src = nil
	}
	if dstReason != "" {
		pair.dst = nil
	}

	reason := firstNonEmpty(srcReason, dstReason)
	if pair.src == nil && pair.dst == nil {
		a.logger.Infof("Skipping generated Application [%s]: %s", ui.Cyan(pair.name), reason)
		return pair, false, nil
	}
	if reason != "" {
		a.logger.Infof("Comparing generated Application [%s] on one branch only: %s", ui.Cyan(pair.name), reason)
	}

	return pair, true, nil
}

// anchoredLegSkipReason names why one comparison leg is outside an anchor's
// reach, or "" when it is comparable — a leg with no Application included. A
// registry chart cannot be affected by a change to this repository, and a
// path-based source belonging to another repository would render from the wrong tree.
func anchoredLegSkipReason(app *models.Application, originURL string) (string, error) {
	if app == nil {
		return "", nil
	}

	target := Target{App: *app}
	if err := target.ClassifySources(); err != nil {
		return "", err
	}
	if !target.PathBased() {
		localRef, err := refsThisRepo(&target, originURL)
		if err != nil {
			return "", err
		}
		if localRef {
			return "", nil
		}
		return "its source is a registry chart, which a change to this repository cannot affect", nil
	}
	for _, source := range target.pathSources() {
		if source == nil || repoIdentityMatches(source.RepoURL, originURL) {
			continue
		}
		return fmt.Sprintf("its source repository %q is not this one, so this repository's tree cannot render it",
			redactRepo(source.RepoURL)), nil
	}

	return "", nil
}

// firstNonEmpty returns the first non-empty string, or "" when both are empty.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

// anchoredExpansionTrees returns the trees the two comparison legs expand an
// anchored ApplicationSet against: the local branch legs normally, or the
// anchored repository's clone for both when that is what its git generators
// read.
func (a *App) anchoredExpansionTrees(repo *GitRepo, manifest ports.AnchoredManifest, ref anchor.ApplicationRef, originURL string) (src, dst ports.RepoTree, err error) {
	remote, err := anchoredGeneratorTree(manifest, ref, originURL, a.cfg.TargetBranch)
	if err != nil {
		return nil, nil, err
	}
	if remote != nil {
		return remote, remote, nil
	}

	headTree, err := repo.HeadTree()
	if err != nil {
		return nil, nil, err
	}
	baseTree, err := repo.MergeBaseTreeFor(a.cfg.TargetBranch)
	if err != nil {
		return nil, nil, err
	}

	return gitTree{tree: headTree}, gitTree{tree: baseTree}, nil
}

// anchoredGeneratorTree reports the anchored repository's clone when that is
// what the ApplicationSet's git generators list, and nil when the local branch
// legs serve — which is the case for a generator naming this repository, and
// for an ApplicationSet declaring no git generator at all.
func anchoredGeneratorTree(manifest ports.AnchoredManifest, ref anchor.ApplicationRef, originURL, targetBranch string) (ports.RepoTree, error) {
	var local, anchored int

	for _, generator := range manifest.ApplicationSet.Spec.Generators {
		if generator.Git == nil {
			continue
		}

		readsAnchored, err := generatorReadsAnchoredRepo(generator.Git, manifest, ref, originURL, targetBranch)
		if err != nil {
			return nil, err
		}
		if readsAnchored {
			anchored++
			continue
		}
		local++
	}

	// Expansion takes one tree, so serving both would drop a generator's
	// Applications without saying so.
	if local > 0 && anchored > 0 {
		return nil, fmt.Errorf("%w: git generators read both this repository and the anchored one; only one repository per ApplicationSet is supported",
			models.ErrUnsupportedAppConfiguration)
	}
	if anchored > 0 {
		return manifest.Tree, nil
	}

	return nil, nil
}

// generatorReadsAnchoredRepo reports whether one git generator reads the
// repository the anchor fetched the manifest from rather than the repository
// being compared, and rejects a generator naming neither or a revision the
// matching tree does not hold.
func generatorReadsAnchoredRepo(generator *models.GitGenerator, manifest ports.AnchoredManifest, ref anchor.ApplicationRef, originURL, targetBranch string) (bool, error) {
	if repoIdentityMatches(generator.RepoURL, originURL) {
		return false, assertGeneratorRevision(generator.Revision, targetBranch, "the compared branch")
	}

	// A same-repo anchor has no second repository to name, so it gets its own
	// message rather than one quoting an empty URL.
	if ref.Repo == "" {
		return false, fmt.Errorf("%w: git generator repoURL %q is not this repository (%q); no tree of it is available",
			models.ErrUnsupportedAppConfiguration, redactRepo(generator.RepoURL), redactRepo(originURL))
	}
	if !repoIdentityMatches(generator.RepoURL, ref.Repo) {
		return false, fmt.Errorf("%w: git generator repoURL %q is neither this repository (%q) nor the anchored one (%q); no tree of it is available",
			models.ErrUnsupportedAppConfiguration, redactRepo(generator.RepoURL), redactRepo(originURL), redactRepo(ref.Repo))
	}

	if manifest.Tree == nil {
		return false, fmt.Errorf("%w: git generator reads the anchored repository %q but the fetch supplied no tree of it",
			models.ErrUnsupportedAppConfiguration, redactRepo(generator.RepoURL))
	}

	return true, assertGeneratorRevision(generator.Revision, manifest.TreeRevision, "the anchored revision read")
}

// assertGeneratorRevision requires a git generator's revision to be one the
// tree standing in for it actually holds. A revision pinned elsewhere keeps
// ArgoCD generating from a tree nothing here can list, so a directory this
// comparison sees would change nothing it deploys.
func assertGeneratorRevision(revision, held, heldDescription string) error {
	if revision == "" || revision == gitRevisionHEAD || revision == held {
		return nil
	}

	return fmt.Errorf("%w: git generator revision %q is neither HEAD nor %s %q, so that tree cannot stand in for it",
		models.ErrUnsupportedAppConfiguration, revision, heldDescription, held)
}

// expandAnchoredApplicationSet expands appSet against one comparison leg's
// tree, naming the leg if expansion fails.
func expandAnchoredApplicationSet(appSet *models.ApplicationSet, tree ports.RepoTree, ref anchor.ApplicationRef, leg string) ([]models.Application, error) {
	apps, err := appset.Expand(appSet, tree)
	if err != nil {
		return nil, fmt.Errorf("expand anchored ApplicationSet %s for %s leg: %w", anchorRefDisplay(ref), leg, err)
	}

	return apps, nil
}

// processAnchoredApplication renders, diffs, and validates the Application that
// the anchor points to. tmpDir is created fresh per group and cleaned up at end.
func (a *App) processAnchoredApplication(ctx context.Context, repo *GitRepo, group AnchorGroup, app models.Application, repoRoot, originURL string) (validationFailed bool, err error) {
	if err := a.checkAnchoredApplication(group, app, repoRoot, originURL); err != nil {
		return false, err
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

	lc := &anchorLegContext{
		app:      app,
		tmpDir:   tmpDir,
		repo:     repo,
		repoRoot: repoRoot,
		results:  validationResults,
	}

	proceed, err := a.renderAnchorLegs(ctx, lc, group)
	if err != nil {
		return false, err
	}
	if !proceed {
		return false, nil
	}

	if err = a.runComparison(ctx, tmpDir, group.Anchor.Application.Path, validationResults); err != nil {
		return false, err
	}

	return anyValidationFailed(validationResults), nil
}

// checkAnchoredApplication rejects an anchored Application the anchor flow cannot render: mixed
// sources, a registry chart taking no values from this repository, a chart in another repository,
// or a cross-repo Application naming a values file this branch's chart no longer has.
func (a *App) checkAnchoredApplication(group AnchorGroup, app models.Application, repoRoot, originURL string) error {
	target := Target{App: app}
	if err := target.ClassifySources(); err != nil {
		return err
	}
	if !target.PathBased() {
		localRef, err := refsThisRepo(&target, originURL)
		if err != nil {
			return err
		}
		if !localRef {
			return fmt.Errorf("%w: %s", ErrAnchorNotPathBased, anchorRefDisplay(group.Anchor.Application))
		}
	}
	if err := assertSameRepo(app.Spec.Source, app.Spec.Sources, originURL); err != nil {
		return fmt.Errorf("%w: %w", ErrAnchorRepoMismatch, err)
	}
	// A cross-repo Application is read from its branch tip while the chart comes from this
	// branch, so a values file the PR renamed fails here legibly rather than inside helm.
	if group.Anchor.Application.Repo != "" {
		return target.checkSourceValueFilesPresent(a.fs, repoRoot, group.Anchor.Application)
	}
	return nil
}

// anchorLegContext carries the per-group state shared by the source and destination legs.
type anchorLegContext struct {
	app      models.Application
	tmpDir   string
	repo     *GitRepo
	repoRoot string
	results  map[string]ports.ValidationResult
}

// renderAnchorLegs renders the source and then the destination leg for an anchor group. It
// returns proceed=false when the anchored chart is absent from the merge-base tree (the
// Application is new on this branch) and --print-added-manifests is off, so there is no
// baseline to diff against.
func (a *App) renderAnchorLegs(ctx context.Context, lc *anchorLegContext, group AnchorGroup) (bool, error) {
	if err := a.renderLeg(ctx, lc.repo, a.newTarget(TargetTypeSource, lc.tmpDir, lc.app), lc.repoRoot, lc.results); err != nil {
		return false, err
	}

	destErr := a.renderLeg(ctx, lc.repo, a.newTarget(TargetTypeDestination, lc.tmpDir, lc.app), lc.repoRoot, lc.results)
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

// checkSourceValueFilesPresent reports ErrValueFileMissingFromSource when a path-based source
// names a chart-relative values file absent from the chart under repoRoot. Each source's path is
// resolved against repoRoot first. A source setting ignoreMissingValueFiles then skips the file
// check, as do entries the renderer validates itself (absolute, "..", empty) and "$ref" entries.
func (t *Target) checkSourceValueFilesPresent(fs afero.Fs, repoRoot string, ref anchor.ApplicationRef) error {
	for _, src := range t.pathSources() {
		chartDir, err := resolveRepoPath(repoRoot, src.Path)
		if err != nil {
			return err
		}
		// A source that opted into dropping missing values files cannot be out of sync with
		// the chart: ArgoCD renders it either way.
		if src.Helm.IgnoreMissingValueFiles {
			continue
		}
		// A chart dir that is absent, or is not a real directory, is materialization's error to
		// report; probing through a symlink would reveal what its target holds.
		info, err := lstatEntry(fs, chartDir)
		if err != nil {
			return fmt.Errorf("check anchored chart dir %q: %w", src.Path, err)
		}
		if info == nil || !info.IsDir() {
			continue
		}
		missing, err := firstMissingValueFile(fs, chartDir, src.Helm.ValueFiles)
		if err != nil {
			return err
		}
		if missing != "" {
			return fmt.Errorf(
				"%w: %q (referenced by anchored Application %s) is not present in %q on the current branch. "+
					"This usually means the pull request restructured the chart's values files, but the Application definition — read from the anchored repo's branch tip — still points at the old layout. "+
					"Update spec.source.helm.valueFiles in that Application to match, and land it (see docs/anchored-repositories.md)",
				ErrValueFileMissingFromSource, missing, anchorRefDisplay(ref), src.Path)
		}
	}
	return nil
}

// firstMissingValueFile returns the first chart-relative entry of valueFiles that does not exist
// under chartDir, or "" when all of them do.
func firstMissingValueFile(fs afero.Fs, chartDir string, valueFiles []string) (string, error) {
	for _, vf := range valueFiles {
		if !isChartRelativeValueFile(vf) {
			continue
		}
		exists, err := valueFileExists(fs, chartDir, vf)
		if err != nil {
			return "", fmt.Errorf("check anchored values file %q: %w", vf, err)
		}
		if !exists {
			return vf, nil
		}
	}
	return "", nil
}

// valueFileExists reports whether vf exists under chartDir along a path of real directories. A
// symlink at any component counts as present: materialization rejects any symlink at or under
// the chart dir uniformly, and looking through one would reveal what exists on the runner.
func valueFileExists(fs afero.Fs, chartDir, vf string) (bool, error) {
	path := chartDir
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(vf)), "/") {
		path = filepath.Join(path, part)
		info, err := lstatEntry(fs, path)
		if err != nil {
			return false, err
		}
		if info == nil {
			return false, nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
	}
	return true, nil
}

// lstatEntry returns the info for path without following a symlink at its final component, or
// nil when path names nothing.
func lstatEntry(fs afero.Fs, path string) (os.FileInfo, error) {
	lstater, ok := fs.(afero.Lstater)
	if !ok {
		return nil, fmt.Errorf("filesystem %T cannot lstat", fs)
	}
	info, _, err := lstater.LstatIfPossible(path)
	switch {
	case err == nil:
		return info, nil
	case os.IsNotExist(err):
		return nil, nil
	default:
		return nil, err
	}
}

// isChartRelativeValueFile reports whether vf is a plain path inside the chart directory.
// Absolute, "..", and empty entries are left for the renderer to reject; "$ref" entries belong
// to a ref source.
func isChartRelativeValueFile(vf string) bool {
	return vf != "" && !filepath.IsAbs(vf) && !strings.HasPrefix(filepath.Clean(vf), "..") && !strings.HasPrefix(vf, "$")
}

// applicationFetcher returns the configured fetcher or builds a default real
// implementation on demand. Tests override this via Dependencies.
func (a *App) applicationFetcher() ports.ApplicationFetcher {
	if a.fetcher != nil {
		return a.fetcher
	}
	return &RealApplicationFetcher{
		FileReader:  a.fileReader,
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
