package app

import (
	"context"
	"fmt"
	"net/url"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/anchor"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/spf13/afero"
)

// RealApplicationFetcher implements ports.ApplicationFetcher.
//
// Same-repo fetches read the file directly from localRepoRoot using the
// existing FileReader port (no Git plumbing involved). Cross-repo fetches
// perform an in-memory clone of Repo at Branch tip (or the remote's default
// branch when Branch is empty) and read the manifest from the resulting
// tree.
//
// Auth precedence for cross-repo clones:
//  1. If GitUsername AND GitToken are both non-empty, an HTTP Basic auth
//     header is attached to the clone — the canonical PAT path for GitHub,
//     GitLab, Bitbucket, and Gitea (go-git's TokenAuth is Bearer and is
//     not what those providers want; see go-git transport/http/common.go).
//  2. Otherwise no Auth is set and go-git falls back to its defaults — SSH
//     agent + default keys for ssh:// URLs, unauthenticated for https://.
//     This preserves the pre-PAT behavior for local development.
type RealApplicationFetcher struct {
	FS          afero.Fs
	FileReader  ports.FileReader
	CmdRunner   ports.CmdRunner
	Log         *logger.Logger
	GitUsername string
	GitToken    string
}

// Fetch resolves ref to a parsed Application.
func (f *RealApplicationFetcher) Fetch(ctx context.Context, ref anchor.ApplicationRef, localRepoRoot string) (models.Application, error) {
	if ref.Repo == "" {
		return f.fetchFromLocal(ref.Path, localRepoRoot)
	}
	return f.fetchFromRemote(ctx, ref)
}

// fetchFromLocal reads ref.Path under localRepoRoot through the same Target.parse
// pipeline used for in-repo Application files. The path is constrained to stay
// within localRepoRoot — a same-repo anchor pointing at `../../etc/passwd`
// would otherwise resolve to a file outside the project. The threat is low
// (the attacker already needs commit access to plant the anchor), but the
// guard matches the symmetric defense in MaterializeTreeDir, and is shared
// with MaterializeChartFromWorkingTree via resolveRepoPath.
func (f *RealApplicationFetcher) fetchFromLocal(path, localRepoRoot string) (models.Application, error) {
	abs, err := resolveRepoPath(localRepoRoot, path)
	if err != nil {
		return models.Application{}, fmt.Errorf("anchor application.path %q: %w", path, err)
	}
	target := Target{
		CmdRunner:  f.CmdRunner,
		FileReader: f.FileReader,
		Log:        f.Log,
		File:       abs,
	}
	if err := target.parse(); err != nil {
		return models.Application{}, fmt.Errorf("read local Application %q: %w", abs, err)
	}
	return target.App, nil
}

// fetchFromRemote clones ref.Repo into memory at Branch tip and reads ref.Path
// from the resulting tree. The clone happens against memory storage and a
// memfs worktree so nothing touches the local filesystem until the parsed
// content is written to a temp file for Target.parse.
func (f *RealApplicationFetcher) fetchFromRemote(ctx context.Context, ref anchor.ApplicationRef) (models.Application, error) {
	cloneOpts := f.buildCloneOptions(ref)

	safeRepo := redactRepo(ref.Repo)
	repo, err := git.CloneContext(ctx, memory.NewStorage(), memfs.New(), cloneOpts)
	if err != nil {
		return models.Application{}, fmt.Errorf("clone %s: %w", safeRepo, err)
	}

	head, err := repo.Head()
	if err != nil {
		return models.Application{}, fmt.Errorf("resolve HEAD of %s: %w", safeRepo, err)
	}

	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return models.Application{}, fmt.Errorf("read HEAD commit of %s: %w", safeRepo, err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return models.Application{}, fmt.Errorf("read HEAD tree of %s: %w", safeRepo, err)
	}

	file, err := tree.File(ref.Path)
	if err != nil {
		return models.Application{}, fmt.Errorf("read %s from %s: %w", ref.Path, safeRepo, err)
	}

	content, err := file.Contents()
	if err != nil {
		return models.Application{}, fmt.Errorf("read contents of %s from %s: %w", ref.Path, safeRepo, err)
	}

	tmpFile, err := helpers.CreateTempFile(f.FS, content)
	if err != nil {
		return models.Application{}, fmt.Errorf("buffer fetched Application: %w", err)
	}
	defer func() {
		if removeErr := afero.Fs.Remove(f.FS, tmpFile.Name()); removeErr != nil {
			f.Log.Errorf("Failed to remove temporary file [%s]: %s", tmpFile.Name(), removeErr)
		}
	}()

	target := Target{
		CmdRunner:  f.CmdRunner,
		FileReader: f.FileReader,
		Log:        f.Log,
		File:       tmpFile.Name(),
	}
	if err := target.parse(); err != nil {
		return models.Application{}, fmt.Errorf("parse Application %s from %s: %w", ref.Path, safeRepo, err)
	}
	return target.App, nil
}

// buildCloneOptions assembles the *git.CloneOptions used by fetchFromRemote.
//
// Auth is attached when GitToken is non-empty. GitUsername defaults to
// "x-access-token" when not set — sufficient for GitHub PATs, GitLab PATs,
// and Gitea. Set GitUsername explicitly for CI_JOB_TOKEN or Bitbucket.
func (f *RealApplicationFetcher) buildCloneOptions(ref anchor.ApplicationRef) *git.CloneOptions {
	opts := &git.CloneOptions{
		URL:          ref.Repo,
		SingleBranch: true,
		Depth:        1,
		Tags:         git.NoTags,
	}
	if ref.Branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(ref.Branch)
	}
	if f.GitToken != "" {
		username := f.GitUsername
		if username == "" {
			username = "x-access-token"
		}
		opts.Auth = &githttp.BasicAuth{Username: username, Password: f.GitToken}
	}
	return opts
}

// redactRepo strips userinfo from a Git URL before it lands in an error or log
// message. Embedding credentials in the URL is unsupported — auth flows either
// via the local Git environment (SSH agent for ssh:// URLs) or via the
// ARGO_COMPARE_GIT_* env vars (BasicAuth for https:// URLs). Defending against
// a URL-embedded credential foot-gun is cheap regardless.
func redactRepo(repo string) string {
	u, err := url.Parse(repo)
	if err != nil || u.User == nil {
		return repo
	}
	u.User = nil
	return u.String()
}
