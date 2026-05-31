package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/anchor"
	"github.com/shini4i/argo-compare/internal/ports/portstest"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleApplicationYAML = `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: example
  namespace: argocd
spec:
  destination:
    server: https://kubernetes.default.svc
    namespace: example
  source:
    repoURL: https://chart.example.com
    chart: example-chart
    targetRevision: 1.0.0
    helm:
      releaseName: example
      values: |
        replicaCount: 1
`

func newTestFetcher(t *testing.T) *RealApplicationFetcher {
	t.Helper()
	return &RealApplicationFetcher{
		FS:         afero.NewOsFs(),
		FileReader: utils.OsFileReader{},
		CmdRunner:  portstest.NoopCmdRunner{},
		Log:        logger.New("fetcher-test-" + t.Name()),
	}
}

func TestFetcher_SameRepo_HappyPath(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "apps"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "apps", "example.yaml"), []byte(sampleApplicationYAML), 0o644))

	f := newTestFetcher(t)
	app, err := f.Fetch(context.Background(), anchor.ApplicationRef{Path: "apps/example.yaml"}, repoRoot)
	require.NoError(t, err)
	assert.Equal(t, "Application", app.Kind)
	assert.Equal(t, "example", app.Metadata.Name)
	assert.Equal(t, "example-chart", app.Spec.Source.Chart)
}

func TestFetcher_SameRepo_FileMissing(t *testing.T) {
	repoRoot := t.TempDir()

	f := newTestFetcher(t)
	_, err := f.Fetch(context.Background(), anchor.ApplicationRef{Path: "apps/missing.yaml"}, repoRoot)
	require.Error(t, err)
}

func TestFetcher_SameRepo_NotAnApplication(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "apps"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "apps", "x.yaml"), []byte("kind: ConfigMap\nmetadata:\n  name: not-an-app\n"), 0o644))

	f := newTestFetcher(t)
	_, err := f.Fetch(context.Background(), anchor.ApplicationRef{Path: "apps/x.yaml"}, repoRoot)
	require.Error(t, err)
}

func TestFetcher_SameRepo_PathEscape(t *testing.T) {
	repoRoot := t.TempDir()
	f := newTestFetcher(t)
	for _, path := range []string{"../escape.yaml", "charts/../../../escape.yaml"} {
		_, err := f.Fetch(context.Background(), anchor.ApplicationRef{Path: path}, repoRoot)
		require.Error(t, err, "path %q must be rejected", path)
		assert.Contains(t, err.Error(), "escapes repository root")
	}
}

func TestFetcher_CrossRepo_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skip cross-repo integration test in short mode")
	}

	tempDir := t.TempDir()
	bareDir := filepath.Join(tempDir, "remote.git")
	require.NoError(t, seedBareRepoWithApplication(t, bareDir, "main", "apps/example.yaml", sampleApplicationYAML))

	f := newTestFetcher(t)
	app, err := f.Fetch(context.Background(), anchor.ApplicationRef{
		Repo:   bareDir,
		Path:   "apps/example.yaml",
		Branch: "main",
	}, "")
	require.NoError(t, err)
	assert.Equal(t, "example", app.Metadata.Name)
	assert.Equal(t, "example-chart", app.Spec.Source.Chart)
}

func TestFetcher_CrossRepo_FileMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("skip cross-repo integration test in short mode")
	}

	tempDir := t.TempDir()
	bareDir := filepath.Join(tempDir, "remote.git")
	require.NoError(t, seedBareRepoWithApplication(t, bareDir, "main", "apps/other.yaml", sampleApplicationYAML))

	f := newTestFetcher(t)
	_, err := f.Fetch(context.Background(), anchor.ApplicationRef{
		Repo:   bareDir,
		Path:   "apps/missing.yaml",
		Branch: "main",
	}, "")
	require.Error(t, err)
}

func TestFetcher_CrossRepo_BadURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skip cross-repo integration test in short mode")
	}

	f := newTestFetcher(t)
	_, err := f.Fetch(context.Background(), anchor.ApplicationRef{
		Repo:   "file:///nonexistent/path/that/does/not/exist.git",
		Path:   "apps/x.yaml",
		Branch: "main",
	}, "")
	require.Error(t, err)
}

func TestFetcher_CrossRepo_BadBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("skip cross-repo integration test in short mode")
	}

	tempDir := t.TempDir()
	bareDir := filepath.Join(tempDir, "remote.git")
	require.NoError(t, seedBareRepoWithApplication(t, bareDir, "main", "apps/example.yaml", sampleApplicationYAML))

	f := newTestFetcher(t)
	_, err := f.Fetch(context.Background(), anchor.ApplicationRef{
		Repo:   bareDir,
		Path:   "apps/example.yaml",
		Branch: "nonexistent-branch",
	}, "")
	require.Error(t, err)
}

func TestFetcher_CrossRepo_BranchDefaultsToHEAD(t *testing.T) {
	if testing.Short() {
		t.Skip("skip cross-repo integration test in short mode")
	}

	tempDir := t.TempDir()
	bareDir := filepath.Join(tempDir, "remote.git")
	require.NoError(t, seedBareRepoWithApplication(t, bareDir, "main", "apps/example.yaml", sampleApplicationYAML))

	f := newTestFetcher(t)
	app, err := f.Fetch(context.Background(), anchor.ApplicationRef{
		Repo: bareDir,
		Path: "apps/example.yaml",
		// Branch intentionally omitted.
	}, "")
	require.NoError(t, err)
	assert.Equal(t, "example", app.Metadata.Name)
}

func TestFetcher_buildCloneOptions_NoAuthWhenTokenEmpty(t *testing.T) {
	f := newTestFetcher(t)
	// GitUsername and GitToken intentionally left zero — emulates a runtime
	// with no PAT configured (today's only mode).
	opts := f.buildCloneOptions(anchor.ApplicationRef{
		Repo:   "https://github.com/example/repo.git",
		Path:   "apps/example.yaml",
		Branch: "main",
	})
	assert.Nil(t, opts.Auth, "no Auth must be set when GitToken is empty (backwards compat with local-Git auth flows)")
	assert.Equal(t, "https://github.com/example/repo.git", opts.URL)
	assert.Equal(t, plumbing.NewBranchReferenceName("main"), opts.ReferenceName)
	assert.True(t, opts.SingleBranch)
	assert.Equal(t, 1, opts.Depth)
	assert.Equal(t, git.NoTags, opts.Tags)
}

func TestFetcher_buildCloneOptions_NoAuthWhenOnlyUsername(t *testing.T) {
	f := newTestFetcher(t)
	f.GitUsername = "x-access-token"
	// Token missing — must NOT set auth (username alone is meaningless and
	// silently sending a blank password would be confusing).
	opts := f.buildCloneOptions(anchor.ApplicationRef{
		Repo: "https://github.com/example/repo.git",
		Path: "apps/example.yaml",
	})
	assert.Nil(t, opts.Auth)
}

func TestFetcher_buildCloneOptions_BasicAuthWhenBothSet(t *testing.T) {
	f := newTestFetcher(t)
	f.GitUsername = "x-access-token"
	f.GitToken = "ghp_secret"

	opts := f.buildCloneOptions(anchor.ApplicationRef{
		Repo: "https://github.com/example/repo.git",
		Path: "apps/example.yaml",
	})

	require.NotNil(t, opts.Auth)
	basic, ok := opts.Auth.(*githttp.BasicAuth)
	require.True(t, ok, "Auth must be *githttp.BasicAuth (GitHub/GitLab/Gitea/Bitbucket all expect basic, not bearer — see go-git transport/http/common.go:530)")
	assert.Equal(t, "x-access-token", basic.Username)
	assert.Equal(t, "ghp_secret", basic.Password)
}

func TestFetcher_buildCloneOptions_DefaultsUsernameWhenTokenOnly(t *testing.T) {
	f := newTestFetcher(t)
	// Typical CI setup: only token provided. Username defaults to "x-access-token",
	// which works for GitHub PATs, GitLab PATs, and Gitea — the common-case providers.
	f.GitToken = "ghp_secret"

	opts := f.buildCloneOptions(anchor.ApplicationRef{
		Repo: "https://github.com/example/repo.git",
		Path: "apps/example.yaml",
	})

	require.NotNil(t, opts.Auth)
	basic, ok := opts.Auth.(*githttp.BasicAuth)
	require.True(t, ok)
	assert.Equal(t, "x-access-token", basic.Username)
	assert.Equal(t, "ghp_secret", basic.Password)
}

func TestFetcher_buildCloneOptions_OmitsBranchWhenEmpty(t *testing.T) {
	f := newTestFetcher(t)
	opts := f.buildCloneOptions(anchor.ApplicationRef{
		Repo: "https://github.com/example/repo.git",
		Path: "apps/example.yaml",
		// Branch intentionally omitted — go-git uses the remote's default.
	})
	assert.Empty(t, string(opts.ReferenceName))
}

func TestRedactRepo(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://user:token@host.example.com/group/repo.git", "https://host.example.com/group/repo.git"},
		{"https://host.example.com/group/repo.git", "https://host.example.com/group/repo.git"},
		{"ssh://git@host.example.com/group/repo.git", "ssh://host.example.com/group/repo.git"},
		{"git@host.example.com:group/repo.git", "git@host.example.com:group/repo.git"},
		{"", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, redactRepo(c.in), "redactRepo(%q)", c.in)
	}
}

// seedBareRepoWithApplication creates a bare git repo at bareDir and pushes a
// single commit containing the given file with the given content on branch.
// The bare repo's HEAD is set to point at the seeded branch so that clients
// cloning without an explicit ReferenceName resolve to it.
func seedBareRepoWithApplication(t *testing.T, bareDir, branch, filePath, content string) error {
	t.Helper()
	bareRepo, err := git.PlainInit(bareDir, true)
	if err != nil {
		return err
	}
	if err := bareRepo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(branch))); err != nil {
		return err
	}

	workDir := t.TempDir()
	repo, err := git.PlainInit(workDir, false)
	if err != nil {
		return err
	}
	if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(branch))); err != nil {
		return err
	}

	absFile := filepath.Join(workDir, filePath)
	if err := os.MkdirAll(filepath.Dir(absFile), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(absFile, []byte(content), 0o644); err != nil {
		return err
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}
	if _, err := worktree.Add(filePath); err != nil {
		return err
	}
	if _, err := worktree.Commit("seed", &git.CommitOptions{Author: defaultSignature()}); err != nil {
		return err
	}

	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{bareDir}}); err != nil {
		return err
	}
	return repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{config.RefSpec("refs/heads/" + branch + ":refs/heads/" + branch)},
	})
}
