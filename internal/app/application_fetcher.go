package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/shini4i/argo-compare/internal/anchor"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"gopkg.in/yaml.v3"
)

// defaultGitUsername is the placeholder username paired with a token when no
// explicit GitUsername is set — accepted by GitHub PATs, GitLab PATs, and Gitea.
const defaultGitUsername = "x-access-token"

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
	FileReader  ports.FileReader
	GitUsername string
	GitToken    string
}

// Fetch resolves ref to the parsed Application or ApplicationSet it names.
func (f *RealApplicationFetcher) Fetch(ctx context.Context, ref anchor.ApplicationRef, localRepoRoot string) (ports.AnchoredManifest, error) {
	if ref.Repo == "" {
		return f.fetchFromLocal(ref.Path, localRepoRoot)
	}
	return f.fetchFromRemote(ctx, ref)
}

// parseAnchoredManifest decodes an anchored manifest as whichever kind it
// declares. A kind neither flow can render, and an ApplicationSet argo-compare
// cannot expand, both fail: an anchor is an explicit pointer, so honouring it
// partially would hide a broken configuration behind a passing run.
func parseAnchoredManifest(content []byte) (ports.AnchoredManifest, error) {
	appSet, err := parseApplicationSetContent(content)
	switch {
	case err == nil:
		return ports.AnchoredManifest{ApplicationSet: appSet}, nil
	case !errors.Is(err, models.ErrNotApplicationSet):
		return ports.AnchoredManifest{}, err
	}

	app := models.Application{}
	if err := yaml.Unmarshal(content, &app); err != nil {
		return ports.AnchoredManifest{}, err
	}
	if err := app.Validate(); err != nil {
		return ports.AnchoredManifest{}, err
	}

	return ports.AnchoredManifest{Application: &app}, nil
}

// fetchFromLocal reads ref.Path under localRepoRoot. resolveRepoPath keeps the
// path inside localRepoRoot, so a planted anchor naming `../../etc/passwd`
// cannot reach outside the project — the same guard MaterializeTreeDir and
// MaterializeChartFromWorkingTree apply to the paths they are handed.
func (f *RealApplicationFetcher) fetchFromLocal(path, localRepoRoot string) (ports.AnchoredManifest, error) {
	abs, err := resolveRepoPath(localRepoRoot, path)
	if err != nil {
		return ports.AnchoredManifest{}, fmt.Errorf("anchor application.path %q: %w", path, err)
	}

	content, err := f.FileReader.ReadFile(abs)
	if err != nil {
		return ports.AnchoredManifest{}, fmt.Errorf("read anchored manifest %q: %w", abs, err)
	}

	manifest, err := parseAnchoredManifest(content)
	if err != nil {
		return ports.AnchoredManifest{}, fmt.Errorf("read local manifest %q: %w", abs, err)
	}

	return manifest, nil
}

// fetchFromRemote clones ref.Repo into memory at Branch tip and reads ref.Path
// from the resulting tree. Memory storage and a memfs worktree keep the local
// filesystem untouched.
func (f *RealApplicationFetcher) fetchFromRemote(ctx context.Context, ref anchor.ApplicationRef) (ports.AnchoredManifest, error) {
	cloneOpts := f.buildCloneOptions(ref)

	safeRepo := redactRepo(ref.Repo)
	repo, err := git.CloneContext(ctx, memory.NewStorage(), memfs.New(), cloneOpts)
	if err != nil {
		return ports.AnchoredManifest{}, fmt.Errorf("clone %s: %w", safeRepo, err)
	}

	head, err := repo.Head()
	if err != nil {
		return ports.AnchoredManifest{}, fmt.Errorf("resolve HEAD of %s: %w", safeRepo, err)
	}

	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return ports.AnchoredManifest{}, fmt.Errorf("read HEAD commit of %s: %w", safeRepo, err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return ports.AnchoredManifest{}, fmt.Errorf("read HEAD tree of %s: %w", safeRepo, err)
	}

	file, err := tree.File(ref.Path)
	if err != nil {
		return ports.AnchoredManifest{}, fmt.Errorf("read %s from %s: %w", ref.Path, safeRepo, err)
	}

	content, err := file.Contents()
	if err != nil {
		return ports.AnchoredManifest{}, fmt.Errorf("read contents of %s from %s: %w", ref.Path, safeRepo, err)
	}

	manifest, err := parseAnchoredManifest([]byte(content))
	if err != nil {
		return ports.AnchoredManifest{}, fmt.Errorf("parse manifest %s from %s: %w", ref.Path, safeRepo, err)
	}

	return manifest, nil
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
			username = defaultGitUsername
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
