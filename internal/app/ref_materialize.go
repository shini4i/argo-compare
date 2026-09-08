package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/spf13/afero"
)

// ErrRefValueFileMissing is returned when a values file addressed as
// "$ref/path" is absent from the ref source it names.
var ErrRefValueFileMissing = errors.New("values file is missing from ref source")

// Permissions for the materialized ref files, matching the rest of the render
// workspace: traversable directories, owner-writable files.
const (
	refDirPerm  = 0o755
	refFilePerm = 0o644
)

// refTreeResolver resolves a revision of a remote repository to its Git tree.
type refTreeResolver interface {
	Tree(ctx context.Context, repoURL, revision string) (*object.Tree, error)
}

// mergeBaseTreeFn resolves the merge-base tree lazily, so a leg that needs no
// same-repo ref file never pays for the walk.
type mergeBaseTreeFn func() (*object.Tree, error)

// materializeRefSourcesForLeg adapts the repository handles a comparison leg
// holds to materializeRefSources. An empty repoRoot is resolved from the
// working directory. Applications without a "$ref" values file return early,
// so the common single-source path needs neither a repo root nor an origin.
func (a *App) materializeRefSourcesForLeg(ctx context.Context, repo *GitRepo, target *Target, repoRoot string) error {
	used, err := target.usedRefFiles()
	if err != nil {
		return err
	}
	if len(used) == 0 {
		return nil
	}

	if repoRoot == "" {
		if repoRoot, err = GetGitRepoRoot(); err != nil {
			return fmt.Errorf("resolve repo root for $ref values files: %w", err)
		}
	}

	var originURL string
	if repo != nil {
		if originURL, err = repo.OriginURL(); err != nil {
			return fmt.Errorf("read origin to classify $ref values sources of %s: %w", target.App.Metadata.Name, err)
		}
	}

	return a.materializeRefSources(ctx, target, repoRoot, originURL, a.mergeBaseTreeOnce(repo))
}

// mergeBaseTreeOnce resolves the merge-base tree at most once per leg: the walk
// is the same for every ref file, and an ApplicationSet can ask for many.
func (a *App) mergeBaseTreeOnce(repo *GitRepo) mergeBaseTreeFn {
	var (
		tree *object.Tree
		err  error
	)
	return func() (*object.Tree, error) {
		if repo == nil {
			return nil, errors.New("destination leg has no repository to read $ref values files from")
		}
		if tree == nil && err == nil {
			tree, err = repo.MergeBaseTreeFor(a.cfg.TargetBranch)
			if err != nil {
				err = fmt.Errorf("resolve merge-base with %s for $ref values files: %w", a.cfg.TargetBranch, err)
			}
		}
		return tree, err
	}
}

// materializeRefSources writes every "$ref/path" values file referenced by the
// Application into the leg's refs directory, from the working tree (source leg)
// or the merge-base tree (destination leg) for a ref pointing at this
// repository, and from a clone for a ref pointing elsewhere.
func (a *App) materializeRefSources(ctx context.Context, target *Target, repoRoot, originURL string, mergeBaseTree mergeBaseTreeFn) error {
	used, err := target.usedRefFiles()
	if err != nil || len(used) == 0 {
		return err
	}

	if originURL == "" {
		return fmt.Errorf("cannot resolve $ref values files for %s: local repo has no origin remote configured, so a ref source cannot be told apart from an external one",
			target.App.Metadata.Name)
	}

	refs, err := target.refSources()
	if err != nil {
		return err
	}
	for _, rf := range used {
		if err := ctx.Err(); err != nil {
			return err
		}
		content, contentErr := a.refFileContent(ctx, rf, refs[rf.Ref], target.Type, repoRoot, originURL, mergeBaseTree)
		if contentErr != nil {
			return contentErr
		}
		dest := filepath.Join(target.refDir(rf.Ref), rf.Path)
		if err := a.fs.MkdirAll(filepath.Dir(dest), refDirPerm); err != nil {
			return fmt.Errorf("create ref values dir for %q: %w", rf.Path, err)
		}
		if err := afero.WriteFile(a.fs, dest, content, refFilePerm); err != nil {
			return fmt.Errorf("write ref values file %q: %w", rf.Path, err)
		}
	}
	return nil
}

// refFileContent reads one ref-source file for the given leg.
func (a *App) refFileContent(ctx context.Context, rf refFile, source *models.Source, leg, repoRoot, originURL string, mergeBaseTree mergeBaseTreeFn) ([]byte, error) {
	if !repoIdentityMatches(source.RepoURL, originURL) {
		tree, err := a.refTreeResolver(originURL).Tree(ctx, source.RepoURL, source.TargetRevision)
		if err != nil {
			return nil, fmt.Errorf("resolve ref source %q at %q: %w", redactRepo(source.RepoURL), source.TargetRevision, err)
		}
		return refFileFromTree(tree, rf, source)
	}

	// A ref pointing at this repository follows the same merge-base-to-HEAD
	// contract as a path-based source: its targetRevision is ignored, because
	// what the tool compares is what the pull request proposes.
	switch leg {
	case TargetTypeSource:
		return refFileFromWorkingTree(repoRoot, rf, source)
	case TargetTypeDestination:
		if mergeBaseTree == nil {
			return nil, fmt.Errorf("destination leg for ref %q has no merge-base tree", rf.Ref)
		}
		tree, err := mergeBaseTree()
		if err != nil {
			return nil, fmt.Errorf("read ref %q values file %q from the compared revision: %w", rf.Ref, rf.Path, err)
		}
		return refFileFromTree(tree, rf, source)
	default:
		return nil, fmt.Errorf("unknown render leg %q", leg)
	}
}

// refFileFromWorkingTree reads a same-repo ref file from the local checkout
// through an os.Root, so a committed symlink cannot resolve outside the
// repository — a lexical path check cannot see that, and the file it named
// would be rendered into a diff posted as a merge request comment.
func refFileFromWorkingTree(repoRoot string, rf refFile, source *models.Source) ([]byte, error) {
	root, err := os.OpenRoot(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("open repository root for ref values file %q: %w", rf.Path, err)
	}
	defer func() { _ = root.Close() }()

	// Symlinks are rejected outright, matching copyDirOnDisk: a Git tree records
	// a link's target rather than its contents, so the destination leg cannot
	// mirror one and the two legs would diff different things.
	info, err := root.Lstat(rf.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, refFileMissing(rf, source, "the current branch")
		}
		return nil, fmt.Errorf("stat ref values file %q: %w", rf.Path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("ref values file %q has unsupported mode %s; only a regular file is supported",
			rf.Path, info.Mode())
	}

	content, err := root.ReadFile(rf.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, refFileMissing(rf, source, "the current branch")
		}
		return nil, fmt.Errorf("read ref values file %q: %w", rf.Path, err)
	}
	return content, nil
}

// refFileFromTree reads a ref file out of a Git tree.
func refFileFromTree(tree *object.Tree, rf refFile, source *models.Source) ([]byte, error) {
	file, err := tree.File(rf.Path)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return nil, refFileMissing(rf, source, "the compared revision")
		}
		return nil, fmt.Errorf("read ref values file %q: %w", rf.Path, err)
	}
	content, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("read contents of ref values file %q: %w", rf.Path, err)
	}
	return []byte(content), nil
}

// refFileMissing names the file, the ref source declaring it, and which
// revision the lookup missed in.
func refFileMissing(rf refFile, source *models.Source, where string) error {
	return fmt.Errorf("%w: %q is not present in ref %q (%s) on %s",
		ErrRefValueFileMissing, rf.Path, rf.Ref, redactRepo(source.RepoURL), where)
}

// refTreeResolver returns the injected resolver, defaulting to one that clones
// remote ref repositories and caches a tree per repository and revision.
// originURL scopes where the configured Git credentials may be sent.
func (a *App) refTreeResolver(originURL string) refTreeResolver {
	if a.refTrees == nil {
		a.refTrees = &gitRefTreeFetcher{
			username:  a.cfg.GitUsername,
			token:     a.cfg.GitToken,
			originURL: originURL,
			trees:     make(map[string]*object.Tree),
		}
	}
	return a.refTrees
}

// gitRefTreeFetcher shallow-clones a remote ref source into memory. One
// ApplicationSet can generate many Applications sharing a values repository,
// so a tree is cached per repository and revision for the run. originURL is the
// only host the configured credentials are sent to.
type gitRefTreeFetcher struct {
	username  string
	token     string
	originURL string
	trees     map[string]*object.Tree
}

// refTreeCacheKey identifies a clone by the exact URL requested. The lossy
// identity used for origin matching drops the port, which would let two
// different repositories share one cached tree.
func refTreeCacheKey(repoURL, revision string) string {
	return repoURL + "@" + revision
}

// Tree resolves revision in repoURL to its tree, cloning on first use.
func (f *gitRefTreeFetcher) Tree(ctx context.Context, repoURL, revision string) (*object.Tree, error) {
	key := refTreeCacheKey(repoURL, revision)
	if tree, ok := f.trees[key]; ok {
		return tree, nil
	}
	tree, err := f.cloneTree(ctx, repoURL, revision)
	if err != nil {
		return nil, err
	}
	f.trees[key] = tree
	return tree, nil
}

// cloneTree clones repoURL at revision and returns the tree of the resulting
// commit. A revision naming a branch is tried first, then the same name as a
// tag; go-git needs to be told which of the two it is up front.
func (f *gitRefTreeFetcher) cloneTree(ctx context.Context, repoURL, revision string) (*object.Tree, error) {
	if isCommitSHA(revision) {
		return nil, fmt.Errorf("ref source %s pins commit %s; a remote ref source must name a branch or a tag",
			redactRepo(repoURL), revision)
	}

	// Reporting every attempt: a revision that is neither a branch nor a tag
	// otherwise fails with a message naming only the tag lookup.
	var errs []error
	for _, refName := range refCloneCandidates(revision) {
		repo, err := git.CloneContext(ctx, memory.NewStorage(), memfs.New(), f.cloneOptions(repoURL, refName))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", firstNonEmpty(refName.String(), "HEAD"), err))
			continue
		}
		return refCloneTree(repo, revision)
	}
	return nil, fmt.Errorf("clone %s at %q: %w%s", redactRepo(repoURL), revision, errors.Join(errs...), commitPinHint(revision))
}

// Full commit hash lengths in hex, for SHA-1 and SHA-256 repositories.
const (
	sha1HexLen   = 40
	sha256HexLen = 64
)

// minShortSHALen is the shortest abbreviation Git accepts for a commit.
const minShortSHALen = 7

// isCommitSHA reports whether a revision is a full commit hash, which a shallow
// single-reference clone cannot fetch.
func isCommitSHA(revision string) bool {
	return (len(revision) == sha1HexLen || len(revision) == sha256HexLen) && isHex(revision)
}

// commitPinHint names commit pinning as the likely cause when a revision that
// resolved as neither branch nor tag looks like an abbreviated hash.
func commitPinHint(revision string) string {
	if len(revision) >= minShortSHALen && isHex(revision) {
		return " (a remote ref source must name a branch or a tag, not a commit)"
	}
	return ""
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// refCloneCandidates lists the reference names to attempt for a revision. An
// empty revision clones the remote's default branch.
func refCloneCandidates(revision string) []plumbing.ReferenceName {
	if revision == "" || revision == "HEAD" {
		return []plumbing.ReferenceName{""}
	}
	return []plumbing.ReferenceName{
		plumbing.NewBranchReferenceName(revision),
		plumbing.NewTagReferenceName(revision),
	}
}

// refCloneTree resolves revision inside a freshly cloned repository, falling
// back to HEAD when the name is not resolvable as a revision.
func refCloneTree(repo *git.Repository, revision string) (*object.Tree, error) {
	hash, err := repo.ResolveRevision(plumbing.Revision(revision))
	if err != nil || hash == nil {
		head, headErr := repo.Head()
		if headErr != nil {
			return nil, fmt.Errorf("resolve %q: %w", revision, headErr)
		}
		h := head.Hash()
		hash = &h
	}
	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, fmt.Errorf("read commit %s: %w", hash.String(), err)
	}
	return commit.Tree()
}

// cloneOptions builds a shallow single-branch clone. Credentials are attached
// only for a repository on the origin host: repoURL comes from the Application
// manifest, so authenticating against an arbitrary host would send the CI token
// wherever a pull request chose to point.
func (f *gitRefTreeFetcher) cloneOptions(repoURL string, refName plumbing.ReferenceName) *git.CloneOptions {
	opts := &git.CloneOptions{
		URL:           repoURL,
		SingleBranch:  true,
		Depth:         1,
		Tags:          git.NoTags,
		ReferenceName: refName,
	}
	if f.token != "" && sameCredentialEndpoint(repoURL, f.originURL) {
		username := f.username
		if username == "" {
			username = defaultGitUsername
		}
		opts.Auth = &githttp.BasicAuth{Username: username, Password: f.token}
	}
	return opts
}

// sameCredentialEndpoint reports whether repoURL is the same HTTPS endpoint the
// local origin is served from. A token must never travel in cleartext, and a
// different port on a matching host is a different service, so the scheme, the
// host and the port all have to agree before credentials are attached.
func sameCredentialEndpoint(repoURL, originURL string) bool {
	parsed, err := url.Parse(repoURL)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := repoIdentityHost(repoURL)
	if host == "" || host != repoIdentityHost(originURL) {
		return false
	}
	return parsed.Port() == originHTTPSPort(originURL)
}

// originHTTPSPort is the port origin serves HTTPS on, empty meaning the
// default. A non-http(s) origin (ssh, scp-style) implies the default, since a
// token can only ever be sent over HTTPS anyway.
func originHTTPSPort(originURL string) string {
	parsed, err := url.Parse(originURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	return parsed.Port()
}

// repoIdentityHost is the host part of normalizeRepoIdentity's host/path key.
func repoIdentityHost(repoURL string) string {
	identity := normalizeRepoIdentity(repoURL)
	host, _, _ := strings.Cut(identity, "/")
	return host
}
