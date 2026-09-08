package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/anchor"
	"github.com/shini4i/argo-compare/internal/models"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anchoredTreeAppSetYAML carries a git generator, which is what obliges a
// cross-repo fetch to attach a tree. Its repoURL is inert here: the fetcher
// never inspects it, and generatorReadsAnchoredRepo does the matching later.
const anchoredTreeAppSetYAML = `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: demo
  namespace: argocd
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://git.example.com/org/gitops.git
        revision: HEAD
        directories:
          - path: clusters/*
  template:
    metadata:
      name: '{{ .path.basename }}-demo'
      namespace: argocd
    spec:
      destination:
        server: https://kubernetes.default.svc
        namespace: '{{ .path.basename }}'
      source:
        repoURL: https://chart.example.com
        chart: demo-chart
        targetRevision: 1.0.0
`

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
	t.Helper()
	return &RealApplicationFetcher{FileReader: utils.OsFileReader{}}
}

func TestFetcher_SameRepo_HappyPath(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "apps"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "apps", "example.yaml"), []byte(sampleApplicationYAML), 0o644))

	f := newTestFetcher(t)
	manifest, err := f.Fetch(context.Background(), anchor.ApplicationRef{Path: "apps/example.yaml"}, repoRoot)
	require.NoError(t, err)
	require.NotNil(t, manifest.Application)
	assert.Equal(t, "Application", manifest.Application.Kind)
	assert.Equal(t, "example", manifest.Application.Metadata.Name)
	assert.Equal(t, "example-chart", manifest.Application.Spec.Source.Chart)
	assert.Nil(t, manifest.Tree, "a same-repo anchor expands against the local branch legs")
	assert.Empty(t, manifest.TreeRevision)
}

// TestFetcher_SameRepo_AppSetCarriesNoTree pins the nil-Tree contract on the
// local fetch path: a same-repo anchor expands against the branch legs, so
// there is no clone to attach even for an ApplicationSet.
func TestFetcher_SameRepo_AppSetCarriesNoTree(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "apps"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "apps", "demo.yaml"), []byte(anchoredTreeAppSetYAML), 0o644))

	f := newTestFetcher(t)
	manifest, err := f.Fetch(context.Background(), anchor.ApplicationRef{Path: "apps/demo.yaml"}, repoRoot)
	require.NoError(t, err)
	require.NotNil(t, manifest.ApplicationSet)
	assert.Nil(t, manifest.Tree)
	assert.Empty(t, manifest.TreeRevision)
}

func TestFetcher_SameRepo_FileMissing(t *testing.T) {
	repoRoot := t.TempDir()

	f := newTestFetcher(t)
	_, err := f.Fetch(context.Background(), anchor.ApplicationRef{Path: "apps/missing.yaml"}, repoRoot)
	// OsFileReader maps a missing file to no content and no error, so the
	// absence surfaces from validation rather than from the read.
	require.ErrorIs(t, err, models.ErrEmptyFile)
	assert.Contains(t, err.Error(), "read local manifest")
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
	manifest, err := f.Fetch(context.Background(), anchor.ApplicationRef{
		Repo:   bareDir,
		Path:   "apps/example.yaml",
		Branch: "main",
	}, "")
	require.NoError(t, err)
	require.NotNil(t, manifest.Application)
	assert.Equal(t, "example", manifest.Application.Metadata.Name)
	assert.Equal(t, "example-chart", manifest.Application.Spec.Source.Chart)
}

// TestFetcher_CrossRepo_AppSetCarriesTree pins the clone an ApplicationSet's
// git generator is expanded against, including the revision name
// assertGeneratorRevision compares a generator's own against.
func TestFetcher_CrossRepo_AppSetCarriesTree(t *testing.T) {
	if testing.Short() {
		t.Skip("skip cross-repo integration test in short mode")
	}

	tempDir := t.TempDir()
	bareDir := filepath.Join(tempDir, "remote.git")
	require.NoError(t, seedBareRepoWithFiles(t, bareDir, "main", map[string]string{
		"apps/demo.yaml":            anchoredTreeAppSetYAML,
		"clusters/dev/config.yaml":  "cluster: dev\n",
		"clusters/prod/config.yaml": "cluster: prod\n",
	}))

	f := newTestFetcher(t)
	manifest, err := f.Fetch(context.Background(), anchor.ApplicationRef{
		Repo:   bareDir,
		Path:   "apps/demo.yaml",
		Branch: "main",
	}, "")
	require.NoError(t, err)
	require.NotNil(t, manifest.ApplicationSet)
	require.NotNil(t, manifest.Tree, "an anchored ApplicationSet must carry the clone its generator lists")

	assert.Equal(t, "main", manifest.TreeRevision, "the revision must be the branch name, not a full ref path")

	dirs, err := manifest.Tree.Directories()
	require.NoError(t, err)
	assert.Subset(t, dirs, []string{"apps", "clusters", "clusters/dev", "clusters/prod"})

	files, err := manifest.Tree.Files()
	require.NoError(t, err)
	assert.Contains(t, files, "apps/demo.yaml")

	content, err := manifest.Tree.ReadFile("clusters/dev/config.yaml")
	require.NoError(t, err)
	assert.Equal(t, "cluster: dev\n", string(content))
}

// TestFetcher_CrossRepo_TreeRevisionDefaultsToRemoteBranch pins the revision an
// omitted anchor branch resolves to, since a generator's own is matched to it.
func TestFetcher_CrossRepo_TreeRevisionDefaultsToRemoteBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("skip cross-repo integration test in short mode")
	}

	tempDir := t.TempDir()
	bareDir := filepath.Join(tempDir, "remote.git")
	require.NoError(t, seedBareRepoWithApplication(t, bareDir, "trunk", "apps/demo.yaml", anchoredTreeAppSetYAML))

	f := newTestFetcher(t)
	manifest, err := f.Fetch(context.Background(), anchor.ApplicationRef{
		Repo: bareDir,
		Path: "apps/demo.yaml",
		// Branch intentionally omitted.
	}, "")
	require.NoError(t, err)
	require.NotNil(t, manifest.Tree)
	assert.Equal(t, "trunk", manifest.TreeRevision)
}

// TestFetcher_CrossRepo_ApplicationCarriesNoTree keeps the clone out of a plain
// Application's manifest, which has no generator to expand.
func TestFetcher_CrossRepo_ApplicationCarriesNoTree(t *testing.T) {
	if testing.Short() {
		t.Skip("skip cross-repo integration test in short mode")
	}

	tempDir := t.TempDir()
	bareDir := filepath.Join(tempDir, "remote.git")
	require.NoError(t, seedBareRepoWithApplication(t, bareDir, "main", "apps/example.yaml", sampleApplicationYAML))

	f := newTestFetcher(t)
	manifest, err := f.Fetch(context.Background(), anchor.ApplicationRef{
		Repo:   bareDir,
		Path:   "apps/example.yaml",
		Branch: "main",
	}, "")
	require.NoError(t, err)
	require.NotNil(t, manifest.Application)
	assert.Nil(t, manifest.Tree)
	assert.Empty(t, manifest.TreeRevision)
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
	manifest, err := f.Fetch(context.Background(), anchor.ApplicationRef{
		Repo: bareDir,
		Path: "apps/example.yaml",
		// Branch intentionally omitted.
	}, "")
	require.NoError(t, err)
	require.NotNil(t, manifest.Application)
	assert.Equal(t, "example", manifest.Application.Metadata.Name)
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
		{"https://user:token@host.example.com/group/repo.git", "https://host.example.com/group/repo.git"}, // trufflehog:ignore
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

	return seedBareRepoWithFiles(t, bareDir, branch, map[string]string{filePath: content})
}

// seedBareRepoWithFiles is seedBareRepoWithApplication for a whole file set,
// which a git generator needs since it lists the tree rather than one path.
func seedBareRepoWithFiles(t *testing.T, bareDir, branch string, files map[string]string) error {
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

	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}

	for _, filePath := range sortedKeys(files) {
		absFile := filepath.Join(workDir, filePath)
		if err := os.MkdirAll(filepath.Dir(absFile), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(absFile, []byte(files[filePath]), 0o644); err != nil {
			return err
		}
		if _, err := worktree.Add(filePath); err != nil {
			return err
		}
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

func TestParseAnchoredManifest(t *testing.T) {
	const appSetYAML = `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: demo
  namespace: argocd
spec:
  goTemplate: true
  generators:
    - list:
        elements:
          - cluster: dev
  template:
    metadata:
      name: '{{ .cluster }}-demo'
`

	cases := []struct {
		name            string
		content         string
		wantKind        string
		wantErrIs       error
		wantErrContains string
	}{
		{name: "Application", content: sampleApplicationYAML, wantKind: models.KindApplication},
		{name: "ApplicationSet", content: appSetYAML, wantKind: models.KindApplicationSet},
		{name: "another kind", content: "kind: ConfigMap\nmetadata:\n  name: cm\n", wantErrIs: models.ErrNotApplication},
		{name: "empty", content: "", wantErrIs: models.ErrEmptyFile},
		{
			name:            "Application shape the decoder rejects",
			content:         "kind: Application\nspec:\n  source: not-a-map\n",
			wantErrContains: "cannot unmarshal",
		},
		{
			name:      "ApplicationSet without goTemplate",
			content:   strings.Replace(appSetYAML, "  goTemplate: true\n", "", 1),
			wantErrIs: models.ErrUnsupportedAppConfiguration,
		},
		{
			name:      "Application with both chart and path",
			content:   strings.Replace(sampleApplicationYAML, "    chart: example-chart\n", "    chart: example-chart\n    path: charts/demo\n", 1),
			wantErrIs: models.ErrUnsupportedAppConfiguration,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			manifest, err := parseAnchoredManifest([]byte(c.content))
			if c.wantErrIs != nil {
				require.ErrorIs(t, err, c.wantErrIs)
				return
			}
			if c.wantErrContains != "" {
				require.ErrorContains(t, err, c.wantErrContains)
				return
			}

			require.NoError(t, err)
			switch c.wantKind {
			case models.KindApplication:
				require.NotNil(t, manifest.Application)
				assert.Nil(t, manifest.ApplicationSet)
			case models.KindApplicationSet:
				require.NotNil(t, manifest.ApplicationSet)
				assert.Nil(t, manifest.Application)
			}
		})
	}
}
