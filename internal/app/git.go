package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/shini4i/argo-compare/internal/ui"
	"github.com/spf13/afero"
)

// GitRepo wraps interactions with the current repository for diff analysis.
type GitRepo struct {
	repo       *git.Repository
	fs         afero.Fs
	cmdRunner  ports.CmdRunner
	fileReader ports.FileReader
	log        *logger.Logger
}

// ChangedFilesResult encapsulates the changed application files, any invalid
// manifests, and anchor groups discovered from changed files that fall under
// a directory containing an anchor file (default `.argo-compare.yml`).
//
// Every changed *.yaml or *.yml that parses as a valid ArgoCD Application
// (kind: Application) appears in Applications; manifests that fail to parse
// appear in Invalid.
//
// AnchorGroups is populated in addition to Applications, not instead of it.
// A single PR can touch both an Application file and a chart directory that
// sits under an anchor — the caller is responsible for deduplicating before
// rendering, since the anchor flow ultimately points back at an Application.
type ChangedFilesResult struct {
	Applications []string
	Invalid      []string
	AnchorGroups []AnchorGroup
}

// DefaultAnchorFileName is the conventional file name for an anchor config.
// Callers may override via the anchorFileName parameter of GetChangedFiles.
const DefaultAnchorFileName = ".argo-compare.yml"

var (
	errGitFileDoesNotExist = errors.New("file does not exist in target branch")
	// ErrNoCommonAncestor is returned when HEAD and the target branch share no
	// history, meaning the "what did src change since branching off" question
	// has no meaningful answer.
	ErrNoCommonAncestor = errors.New("no common ancestor")
	// ErrAmbiguousMergeBase is returned when the history between HEAD and the
	// target branch has multiple equally-valid merge bases (criss-cross merges).
	// Picking one arbitrarily would produce non-deterministic results, so we
	// surface the ambiguity to the user instead.
	ErrAmbiguousMergeBase = errors.New("ambiguous merge-base")
)

// NewGitRepo opens the Git repository rooted at the current working directory and returns a GitRepo configured with the provided filesystem, command runner, file reader, and logger.
// It locates the repository root and opens the repository; an error is returned if root discovery or repository opening fails.
func NewGitRepo(fs afero.Fs, cmdRunner ports.CmdRunner, fileReader ports.FileReader, log *logger.Logger) (*GitRepo, error) {
	repoRoot, err := GetGitRepoRoot()
	if err != nil {
		return nil, err
	}

	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	return &GitRepo{
		repo:       repo,
		fs:         fs,
		cmdRunner:  cmdRunner,
		fileReader: fileReader,
		log:        log,
	}, nil
}

// GetChangedFiles returns application files that the current branch (HEAD)
// has modified since it diverged from targetBranch. Files modified only on
// targetBranch after the divergence point are intentionally excluded — those
// are not changes the source branch is proposing.
//
// anchorFileName names the file that marks an "anchor" directory (e.g.
// `.argo-compare.yml`). Changed files that fall under such a directory are
// additionally returned in the result's AnchorGroups field. Passing an empty
// string disables anchor discovery — useful for callers that opt out.
func (g *GitRepo) GetChangedFiles(targetBranch string, filesToIgnore []string, anchorFileName string) (ChangedFilesResult, error) {
	targetCommit, err := g.commitForBranch(targetBranch)
	if err != nil {
		return ChangedFilesResult{}, err
	}

	headRef, err := g.repo.Head()
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("failed to get HEAD: %w", err)
	}

	headCommit, err := g.repo.CommitObject(headRef.Hash())
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("failed to get commit object for current branch: %w", err)
	}

	baseTree, err := g.mergeBaseTree(headCommit, targetCommit)
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("failed to resolve merge-base with %s: %w", targetBranch, err)
	}

	headTree, err := headCommit.Tree()
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("failed to get tree for head commit: %w", err)
	}

	changes, err := object.DiffTree(baseTree, headTree)
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("failed to get diff between trees: %w", err)
	}

	foundFiles := make([]string, 0, len(changes))
	removedFiles := make([]string, 0, len(changes))
	for _, change := range changes {
		if change.To.Name == "" {
			removedFiles = append(removedFiles, change.From.Name)
			continue
		}
		foundFiles = append(foundFiles, change.To.Name)
	}

	g.printChangeFile(foundFiles, removedFiles)

	repoRoot, err := GetGitRepoRoot()
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("resolve repo root: %w", err)
	}

	applications, invalid, err := g.sortChangedFiles(foundFiles, repoRoot)
	if err != nil {
		return ChangedFilesResult{}, err
	}
	filtered := filterIgnored(applications, filesToIgnore)

	generated, err := g.discoverGeneratorApplicationSets(repoRoot, targetBranch, foundFiles, removedFiles, filesToIgnore)
	if err != nil {
		return ChangedFilesResult{}, err
	}
	filtered = mergePaths(filtered, generated)

	var anchorGroups []AnchorGroup
	if anchorFileName != "" {
		anchorChanged := filterIgnored(foundFiles, filesToIgnore)
		anchorGroups, err = DiscoverAnchors(repoRoot, anchorChanged, g.fs, anchorFileName)
		if err != nil {
			return ChangedFilesResult{}, err
		}
	}

	return ChangedFilesResult{Applications: filtered, Invalid: invalid, AnchorGroups: anchorGroups}, nil
}

// MergeBaseTreeFor returns the tree of the merge-base commit between HEAD and
// origin/targetBranch. Path-based rendering uses this tree to materialize the
// "before the PR" snapshot of a chart directory while the working tree holds
// the "after the PR" snapshot.
func (g *GitRepo) MergeBaseTreeFor(targetBranch string) (*object.Tree, error) {
	targetCommit, err := g.commitForBranch(targetBranch)
	if err != nil {
		return nil, err
	}
	headRef, err := g.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}
	headCommit, err := g.repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit object for current branch: %w", err)
	}
	return g.mergeBaseTree(headCommit, targetCommit)
}

// commitForBranch resolves the commit at the tip of origin/<branch>. It centralizes
// the ref + commit-object lookup so the surrounding error strings are not duplicated
// across each caller (SonarCloud go:S1192).
func (g *GitRepo) commitForBranch(branch string) (*object.Commit, error) {
	ref, err := g.repo.Reference(plumbing.ReferenceName("refs/remotes/origin/"+branch), true)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve target branch %s: %w", branch, err)
	}
	commit, err := g.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit object for target branch %s: %w", branch, err)
	}
	return commit, nil
}

// OriginURL returns the URL configured for the `origin` remote, or an empty
// string with a nil error when the local repo has no `origin` remote. Callers
// use this to verify that an Application's spec.source.repoURL points back at
// the local repo (path-based v1 requires this).
func (g *GitRepo) OriginURL() (string, error) {
	remote, err := g.repo.Remote("origin")
	if err != nil {
		if errors.Is(err, git.ErrRemoteNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("read origin remote: %w", err)
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return "", nil
	}
	return urls[0], nil
}

// mergeBaseTree returns the tree of the merge-base commit between headCommit
// and targetCommit — the snapshot from which the source branch diverged.
// Diffing against this snapshot yields only the changes the source branch
// actually introduced, ignoring commits made on the target branch since
// divergence.
//
// Returns ErrNoCommonAncestor if the histories are unrelated, or
// ErrAmbiguousMergeBase if multiple equally-valid merge bases exist
// (criss-cross merges) — go-git does not perform recursive merge-base
// resolution, so picking one silently would be non-deterministic.
func (g *GitRepo) mergeBaseTree(headCommit, targetCommit *object.Commit) (*object.Tree, error) {
	bases, err := headCommit.MergeBase(targetCommit)
	if err != nil {
		return nil, fmt.Errorf("failed to find merge-base: %w", err)
	}
	switch len(bases) {
	case 0:
		return nil, ErrNoCommonAncestor
	case 1:
	default:
		return nil, fmt.Errorf("%w: %d candidates (history contains criss-cross merges)", ErrAmbiguousMergeBase, len(bases))
	}

	tree, err := bases[0].Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree for merge-base commit: %w", err)
	}
	return tree, nil
}

// GetChangedFileContent fetches and parses targetFile from targetBranch.
func (g *GitRepo) GetChangedFileContent(targetBranch, targetFile string, printAdded bool) (models.Application, error) {
	g.log.Debugf("Getting content of %s from %s", targetFile, targetBranch)

	targetTree, err := g.treeForBranch(targetBranch)
	if err != nil {
		return models.Application{}, err
	}

	fileContent, err := g.targetFileContent(targetTree, targetBranch, targetFile, printAdded)
	if err != nil {
		return models.Application{}, err
	}

	return g.parseTargetApplication(fileContent)
}

// GetChangedFileRaw returns the unparsed contents of targetFile on
// targetBranch, or errGitFileDoesNotExist when the file is absent there.
func (g *GitRepo) GetChangedFileRaw(targetBranch, targetFile string) ([]byte, error) {
	targetTree, err := g.treeForBranch(targetBranch)
	if err != nil {
		return nil, err
	}

	fileEntry, err := targetTree.File(targetFile)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return nil, errGitFileDoesNotExist
		}
		return nil, fmt.Errorf("failed to find file %s in target branch %s: %w", targetFile, targetBranch, err)
	}

	fileContent, err := fileEntry.Contents()
	if err != nil {
		return nil, fmt.Errorf("failed to get contents of file %s: %w", targetFile, err)
	}

	return []byte(fileContent), nil
}

// treeForBranch resolves the Git tree for the provided remote branch reference.
func (g *GitRepo) treeForBranch(targetBranch string) (*object.Tree, error) {
	targetCommit, err := g.commitForBranch(targetBranch)
	if err != nil {
		return nil, err
	}

	targetTree, err := targetCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree for target commit: %w", err)
	}

	return targetTree, nil
}

// targetFileContent retrieves the contents of a manifest from the target branch, respecting print options.
func (g *GitRepo) targetFileContent(targetTree *object.Tree, targetBranch, targetFile string, printAdded bool) (string, error) {
	fileEntry, err := targetTree.File(targetFile)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			g.log.Warning(ui.Yellow(fmt.Sprintf("The requested file %s does not exist in target branch %s, assuming it is a new Application", targetFile, targetBranch)))
			if !printAdded {
				return "", errGitFileDoesNotExist
			}
			return "", nil
		}
		return "", fmt.Errorf("failed to find file %s in target branch %s: %w", targetFile, targetBranch, err)
	}

	if fileEntry == nil {
		return "", nil
	}

	fileContent, err := fileEntry.Contents()
	if err != nil {
		return "", fmt.Errorf("failed to get contents of file %s: %w", targetFile, err)
	}

	return fileContent, nil
}

// parseTargetApplication parses the retrieved manifest content into an Application model.
func (g *GitRepo) parseTargetApplication(fileContent string) (models.Application, error) {
	tmpFile, err := helpers.CreateTempFile(g.fs, fileContent)
	if err != nil {
		return models.Application{}, err
	}

	defer func(file afero.File) {
		if err := afero.Fs.Remove(g.fs, file.Name()); err != nil {
			g.log.Errorf("Failed to remove temporary file [%s]: %s", file.Name(), err)
		}
	}(tmpFile)

	target := Target{
		CmdRunner:  g.cmdRunner,
		FileReader: g.fileReader,
		Log:        g.log,
		File:       tmpFile.Name(),
	}
	if err := target.parse(); err != nil {
		return models.Application{}, fmt.Errorf("failed to parse the application: %w", err)
	}

	return target.App, nil
}

// printChangeFile reports the lists of added and removed files at debug level.
func (g *GitRepo) printChangeFile(addedFiles, removed []string) {
	g.log.Debug("===> Found the following changed files:")
	for _, file := range addedFiles {
		if file != "" {
			g.log.Debugf("▶ %s", file)
		}
	}
	g.log.Debug("===> Found the following removed files:")
	for _, file := range removed {
		if file != "" {
			g.log.Debugf("▶ %s", ui.Red(file))
		}
	}
}

// isYAMLFile reports whether name carries a YAML extension. ArgoCD accepts
// both `.yaml` and `.yml`, so gating on one alone drops the other silently
// (issue #176).
func isYAMLFile(name string) bool {
	ext := filepath.Ext(name)

	return ext == ".yaml" || ext == ".yml"
}

// sortChangedFiles filters diff results to include only valid Application
// manifests. Helm chart templates are excluded up front: their `{{ }}` actions
// are not valid YAML, so parsing one as an Application would misreport it as
// invalid and fail the run (issue #153).
func (g *GitRepo) sortChangedFiles(files []string, repoRoot string) (applications []string, invalid []string, err error) {
	for _, file := range files {
		if !isYAMLFile(file) {
			continue
		}

		isTemplate, tmplErr := isHelmTemplate(g.fs, repoRoot, file)
		if tmplErr != nil {
			return nil, nil, fmt.Errorf("check whether %q is a Helm template: %w", file, tmplErr)
		}
		if isTemplate {
			g.log.Debugf("Skipping Helm chart template [%s]", file)
			continue
		}

		switch kind, err := g.classifyManifest(file); {
		case errors.Is(err, models.ErrNotApplication):
			g.log.Debugf("Skipping non-application file [%s]", file)
		case errors.Is(err, models.ErrUnsupportedAppConfiguration):
			g.log.Warningf("Skipping unsupported application configuration [%s]: %s", file, err)
		case errors.Is(err, models.ErrEmptyFile):
			g.log.Debugf("Skipping empty file [%s]", file)
		case err != nil:
			g.log.Errorf("Error checking if [%s] is an Application: %s", file, err)
			invalid = append(invalid, file)
		case kind != "":
			applications = append(applications, file)
		}
	}

	if len(applications) > 0 {
		g.log.Info("===> Found the following changed Application files")
		for _, file := range applications {
			g.log.Infof("▶ %s", ui.Yellow(file))
		}
	}

	return applications, invalid, nil
}

// isHelmTemplate reports whether relFile is a Helm chart template — a file that
// lives under a chart's `templates/` directory, where a chart is any directory
// containing Chart.yaml. This mirrors Helm's own definition: everything under
// `templates/` is handed to the rendering engine, and detection is purely by
// location, never by content. Such files are never standalone ArgoCD
// Application manifests, so callers exclude them from Application discovery
// rather than parsing their (invalid-as-YAML) templating syntax.
//
// relFile is a repo-relative, slash-separated path as emitted by the Git diff;
// repoRoot anchors the Chart.yaml lookup to the local worktree.
func isHelmTemplate(fs afero.Fs, repoRoot, relFile string) (bool, error) {
	parts := strings.Split(filepath.ToSlash(filepath.Dir(relFile)), "/")
	for i, part := range parts {
		if part != "templates" {
			continue
		}
		chartRoot := filepath.Join(append([]string{repoRoot}, parts[:i]...)...)
		exists, err := afero.Exists(fs, filepath.Join(chartRoot, "Chart.yaml"))
		if err != nil {
			return false, fmt.Errorf("check Chart.yaml at %q: %w", chartRoot, err)
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

// classifyManifest reports whether file holds a manifest argo-compare can
// process, returning models.KindApplication or models.KindApplicationSet.
// A file that is neither yields ErrNotApplication, so the caller reports it
// with the same message it always has.
func (g *GitRepo) classifyManifest(file string) (string, error) {
	g.log.Debugf("===> Checking if [%s] is an Application", ui.Cyan(file))

	target := Target{
		CmdRunner:  g.cmdRunner,
		FileReader: g.fileReader,
		Log:        g.log,
		File:       file,
	}

	appErr := target.parse()
	if appErr == nil {
		return models.KindApplication, nil
	}
	if !errors.Is(appErr, models.ErrNotApplication) {
		return "", appErr
	}

	switch _, setErr := parseApplicationSet(g.fileReader, file); {
	case setErr == nil:
		return models.KindApplicationSet, nil
	case errors.Is(setErr, models.ErrNotApplicationSet):
		return "", appErr
	default:
		return "", setErr
	}
}

// GetGitRepoRoot returns the filesystem path of the nearest parent directory,
// starting from the current working directory, that contains a Git repository.
// It returns an error if the current working directory cannot be determined or
// if no Git repository is found in any ancestor directories.
func GetGitRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}

	for {
		_, err := git.PlainOpen(dir)
		if err == nil {
			return dir, nil
		}

		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			break
		}

		dir = parentDir
	}

	return "", fmt.Errorf("no git repository found")
}

// filterIgnored filters out files that appear in the ignored list.
// If the ignored list is empty, the input slice is returned unchanged.
// Comparison is by exact string match and the order of remaining files is preserved.
func filterIgnored(files, ignored []string) []string {
	if len(ignored) == 0 {
		return files
	}

	ignoredSet := make(map[string]struct{}, len(ignored))
	for _, file := range ignored {
		ignoredSet[file] = struct{}{}
	}

	filtered := make([]string, 0, len(files))
	for _, file := range files {
		if _, ok := ignoredSet[file]; ok {
			continue
		}
		filtered = append(filtered, file)
	}

	return filtered
}

// HeadTree returns the tree of the commit at HEAD. Directory listing reads it
// rather than the filesystem so both comparison legs see the same universe:
// the working tree also holds untracked and ignored files, which Git does not
// record and ArgoCD would never deploy.
func (g *GitRepo) HeadTree() (*object.Tree, error) {
	headRef, err := g.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	headCommit, err := g.repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit object for current branch: %w", err)
	}

	tree, err := headCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree for head commit: %w", err)
	}

	return tree, nil
}
