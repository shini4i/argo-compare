package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/anchor"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppRunAnchorFlowSameRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache")
	tmpBase := filepath.Join(tempDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpBase, 0o755))

	remoteDir := filepath.Join(tempDir, "origin.git")
	bareRepo, err := git.PlainInit(remoteDir, true)
	require.NoError(t, err)
	require.NoError(t, bareRepo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	workDir := filepath.Join(tempDir, "work")
	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	originURL := "file://" + remoteDir
	require.NoError(t, writePathBasedAppFixture(workDir, originURL, 1))

	worktree, err := repo.Worktree()
	require.NoError(t, err)
	_, err = worktree.Add(".")
	require.NoError(t, err)
	initialHash, err := worktree.Commit("initial", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{originURL}})
	require.NoError(t, err)
	require.NoError(t, repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{"refs/heads/main:refs/heads/main"},
	}))
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), initialHash)))

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature/anchor"),
		Create: true,
	}))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "charts", "demo", "values.yaml"), []byte("replicaCount: 7\n"), 0o644))
	_, err = worktree.Add("charts/demo/values.yaml")
	require.NoError(t, err)
	_, err = worktree.Commit("bump replicas", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWD)) })

	var logBuffer bytes.Buffer
	logger.RedirectForTest(t, &logBuffer)
	appLogger := logger.New("app-anchor-test")

	helmStub := newStubHelmProcessor(t)

	cfg := Config{
		TargetBranch:        "main",
		CacheDir:            cacheDir,
		TempDirBase:         tmpBase,
		PrintAddedManifests: true,
		Version:             "test",
		AnchorFileName:      DefaultAnchorFileName,
	}
	appInstance, err := New(cfg, Dependencies{
		FS:            afero.NewOsFs(),
		CmdRunner:     &stubCmdRunner{},
		FileReader:    utils.OsFileReader{},
		HelmProcessor: helmStub,
		Globber:       utils.CustomGlobber{},
		Logger:        appLogger,
	})
	require.NoError(t, err)

	require.NoError(t, appInstance.Run(context.Background()))

	// Anchor flow renders both legs, but Helm is stubbed so the only signal we
	// can assert here is that the render entry log appeared and that
	// RenderAppSource was invoked twice (src + dst). The detailed templates
	// come from stub manifests written by stubHelmProcessor.
	assert.Equal(t, 2, helmStub.callCount("RenderAppSource"), "anchor flow must render src and dst legs")
	assert.Equal(t, 2, helmStub.callCount("GenerateValuesFile"), "anchor flow must produce values files for both legs")
	// Download/Extract must NOT be called for a path-based source.
	assert.Equal(t, 0, helmStub.callCount("DownloadHelmChart"), "path-based source must not trigger helm pull")
	assert.Equal(t, 0, helmStub.callCount("ExtractHelmChart"), "path-based source must not extract a tarball")
	assert.Contains(t, logBuffer.String(), "Processing anchored chart")
}

// writePathBasedAppFixture lays out a same-repo all-in-one structure: a path-
// based Application manifest plus an umbrella chart it points at, plus an
// .argo-compare.yml in the chart dir pointing back at the Application.
func writePathBasedAppFixture(repoDir, originURL string, replicas int) error {
	appsDir := filepath.Join(repoDir, "apps")
	chartDir := filepath.Join(repoDir, "charts", "demo")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		return err
	}

	app := `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: demo
  namespace: argocd
spec:
  destination:
    server: https://kubernetes.default.svc
    namespace: demo
  source:
    repoURL: ` + originURL + `
    path: charts/demo
    targetRevision: HEAD
    helm:
      releaseName: demo
      values: |
        extraValue: from-app
`
	if err := os.WriteFile(filepath.Join(appsDir, "demo.yaml"), []byte(app), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("apiVersion: v2\nname: demo\nversion: 0.0.1\n"), 0o644); err != nil {
		return err
	}
	values := []byte("replicaCount: ")
	switch replicas {
	case 0:
		values = append(values, '0')
	default:
		values = append(values, byte('0'+replicas))
	}
	values = append(values, '\n')
	if err := os.WriteFile(filepath.Join(chartDir, "values.yaml"), values, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "dep.yaml"), []byte("kind: Deployment\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(chartDir, ".argo-compare.yml"), []byte("application:\n  path: apps/demo.yaml\n"), 0o644)
}

func TestDedupAnchorGroups(t *testing.T) {
	groups := []AnchorGroup{
		{Dir: "/repo/charts/foo", Anchor: dedupAnchor("apps/foo.yaml", "")},
		{Dir: "/repo/charts/bar", Anchor: dedupAnchor("apps/bar.yaml", "")},
		{Dir: "/repo/charts/baz", Anchor: dedupAnchor("apps/baz.yaml", "ssh://git@example.com/group/repo.git")},
	}
	// "apps/foo.yaml" is both an anchor target AND in changedApps → drop foo's group.
	// "apps/baz.yaml" is a cross-repo anchor → never dropped, even when listed.
	deduped := dedupAnchorGroups(groups, []string{"apps/foo.yaml", "apps/baz.yaml"})
	require.Len(t, deduped, 2)
	dirs := []string{deduped[0].Dir, deduped[1].Dir}
	assert.NotContains(t, dirs, "/repo/charts/foo")
	assert.Contains(t, dirs, "/repo/charts/bar")
	assert.Contains(t, dirs, "/repo/charts/baz")
}

func TestDedupAnchorGroups_PathVariantsMatch(t *testing.T) {
	// Anchor spells the Application with a leading "./", changedApps lists it
	// without. After filepath.Clean both sides, dedup must still fire.
	groups := []AnchorGroup{
		{Dir: "/repo/charts/foo", Anchor: dedupAnchor("./apps/foo.yaml", "")},
	}
	deduped := dedupAnchorGroups(groups, []string{"apps/foo.yaml"})
	assert.Empty(t, deduped, "./apps/foo.yaml must dedup against apps/foo.yaml")
}

func dedupAnchor(path, repo string) anchor.Anchor {
	return anchor.Anchor{
		Application: anchor.ApplicationRef{Path: path, Repo: repo},
	}
}

// TestAppRunPathBasedApplicationFileInDiff covers the case where the changed
// file in the diff IS the path-based Application manifest (rather than a chart
// file underneath it). Before the routing fix, this Application would land in
// the chart pipeline and fail at helm pull with an empty chart name. After
// the fix, the existing changed-file flow recognizes path-based sources and
// routes them through MaterializeChartFromWorkingTree / MaterializeChartFromTree.
func TestAppRunPathBasedApplicationFileInDiff(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache")
	tmpBase := filepath.Join(tempDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpBase, 0o755))

	remoteDir := filepath.Join(tempDir, "origin.git")
	bareRepo, err := git.PlainInit(remoteDir, true)
	require.NoError(t, err)
	require.NoError(t, bareRepo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	workDir := filepath.Join(tempDir, "work")
	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	originURL := "file://" + remoteDir
	require.NoError(t, writePathBasedAppFixture(workDir, originURL, 1))

	worktree, err := repo.Worktree()
	require.NoError(t, err)
	_, err = worktree.Add(".")
	require.NoError(t, err)
	initialHash, err := worktree.Commit("initial", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{originURL}})
	require.NoError(t, err)
	require.NoError(t, repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{"refs/heads/main:refs/heads/main"},
	}))
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), initialHash)))

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature/app-edit"),
		Create: true,
	}))
	// Modify the Application file itself (not chart contents). This is the
	// path that previously hit the chart pipeline and failed.
	appBody, err := os.ReadFile(filepath.Join(workDir, "apps", "demo.yaml"))
	require.NoError(t, err)
	mutated := bytes.ReplaceAll(appBody, []byte("releaseName: demo"), []byte("releaseName: demo-v2"))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "apps", "demo.yaml"), mutated, 0o644))
	_, err = worktree.Add("apps/demo.yaml")
	require.NoError(t, err)
	_, err = worktree.Commit("rename release", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWD)) })

	logger.RedirectForTest(t, new(bytes.Buffer))
	appLogger := logger.New("app-pathbased-diff-test")

	helmStub := newStubHelmProcessor(t)
	cfg := Config{
		TargetBranch:        "main",
		CacheDir:            cacheDir,
		TempDirBase:         tmpBase,
		PrintAddedManifests: true,
		Version:             "test",
		AnchorFileName:      DefaultAnchorFileName,
	}
	appInstance, err := New(cfg, Dependencies{
		FS:            afero.NewOsFs(),
		CmdRunner:     &stubCmdRunner{},
		FileReader:    utils.OsFileReader{},
		HelmProcessor: helmStub,
		Globber:       utils.CustomGlobber{},
		Logger:        appLogger,
	})
	require.NoError(t, err)

	require.NoError(t, appInstance.Run(context.Background()))

	// Path-based source MUST NOT trigger helm pull / extract.
	assert.Equal(t, 0, helmStub.callCount("DownloadHelmChart"), "path-based Application must not trigger helm pull")
	assert.Equal(t, 0, helmStub.callCount("ExtractHelmChart"), "path-based Application must not extract a tarball")
	// Both legs must still render.
	assert.Equal(t, 2, helmStub.callCount("RenderAppSource"), "src and dst legs must both render")
}

// runAnchorFailureScenario sets up a same-repo path-based fixture, lets the
// caller mutate files just before the initial commit (to provoke a specific
// failure mode), then runs App.Run on a feature branch that bumps the chart
// values (so the anchor flow triggers via the changed chart file). It returns
// the terminal error from App.Run for the caller to assert errors.Is against.
//
// The helper backs the four failure-mode tests below (ErrAnchorNotPathBased,
// ErrAnchorRepoMismatch, anchor.ErrInvalidAnchor, ErrMixedMultiSource) so each
// test focuses on the mutation that triggers its sentinel.
func runAnchorFailureScenario(t *testing.T, mutate func(workDir, originURL string)) error {
	t.Helper()

	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache")
	tmpBase := filepath.Join(tempDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpBase, 0o755))

	remoteDir := filepath.Join(tempDir, "origin.git")
	bareRepo, err := git.PlainInit(remoteDir, true)
	require.NoError(t, err)
	require.NoError(t, bareRepo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	workDir := filepath.Join(tempDir, "work")
	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	originURL := "file://" + remoteDir
	require.NoError(t, writePathBasedAppFixture(workDir, originURL, 1))

	if mutate != nil {
		mutate(workDir, originURL)
	}

	worktree, err := repo.Worktree()
	require.NoError(t, err)
	_, err = worktree.Add(".")
	require.NoError(t, err)
	initialHash, err := worktree.Commit("initial", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{originURL}})
	require.NoError(t, err)
	require.NoError(t, repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{"refs/heads/main:refs/heads/main"},
	}))
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), initialHash)))

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature/anchor-fail"),
		Create: true,
	}))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "charts", "demo", "values.yaml"), []byte("replicaCount: 9\n"), 0o644))
	_, err = worktree.Add("charts/demo/values.yaml")
	require.NoError(t, err)
	_, err = worktree.Commit("bump replicas", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWD)) })

	logger.RedirectForTest(t, new(bytes.Buffer))
	appLogger := logger.New("anchor-fail-" + t.Name())

	helmStub := newStubHelmProcessor(t)
	cfg := Config{
		TargetBranch:        "main",
		CacheDir:            cacheDir,
		TempDirBase:         tmpBase,
		PrintAddedManifests: true,
		Version:             "test",
		AnchorFileName:      DefaultAnchorFileName,
	}
	appInstance, err := New(cfg, Dependencies{
		FS:            afero.NewOsFs(),
		CmdRunner:     &stubCmdRunner{},
		FileReader:    utils.OsFileReader{},
		HelmProcessor: helmStub,
		Globber:       utils.CustomGlobber{},
		Logger:        appLogger,
	})
	require.NoError(t, err)

	return appInstance.Run(context.Background())
}

func TestAppRunAnchorFlow_NotPathBased(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}
	err := runAnchorFailureScenario(t, func(workDir, originURL string) {
		chartBased := `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: demo
  namespace: argocd
spec:
  destination:
    server: https://kubernetes.default.svc
    namespace: demo
  source:
    repoURL: https://charts.example.com
    chart: demo
    targetRevision: 0.0.1
    helm:
      releaseName: demo
`
		require.NoError(t, os.WriteFile(filepath.Join(workDir, "apps", "demo.yaml"), []byte(chartBased), 0o644))
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAnchorNotPathBased)
}

func TestAppRunAnchorFlow_RepoMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}
	err := runAnchorFailureScenario(t, func(workDir, originURL string) {
		// Application is path-based but repoURL points at a different repo.
		// assertSameRepo must reject before any chart materialization.
		mismatched := `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: demo
  namespace: argocd
spec:
  destination:
    server: https://kubernetes.default.svc
    namespace: demo
  source:
    repoURL: https://other.example.com/group/other.git
    path: charts/demo
    targetRevision: HEAD
    helm:
      releaseName: demo
`
		require.NoError(t, os.WriteFile(filepath.Join(workDir, "apps", "demo.yaml"), []byte(mismatched), 0o644))
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAnchorRepoMismatch)
}

func TestAppRunAnchorFlow_InvalidAnchor(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}
	err := runAnchorFailureScenario(t, func(workDir, originURL string) {
		// Malformed YAML — anchor.Load must wrap ErrInvalidAnchor and the
		// error must propagate through DiscoverAnchors → App.Run.
		require.NoError(t, os.WriteFile(
			filepath.Join(workDir, "charts", "demo", ".argo-compare.yml"),
			[]byte("application:\n  : missing key\n  path:\n    - not a string\n"),
			0o644,
		))
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, anchor.ErrInvalidAnchor)
}

func TestAppRunAnchorFlow_MixedMultiSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}
	err := runAnchorFailureScenario(t, func(workDir, originURL string) {
		// Multi-source Application mixing chart-based and path-based sources.
		// ClassifySources must reject before PathBased check.
		mixed := `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: demo
  namespace: argocd
spec:
  destination:
    server: https://kubernetes.default.svc
    namespace: demo
  sources:
    - repoURL: ` + originURL + `
      path: charts/demo
      targetRevision: HEAD
      helm:
        releaseName: demo
    - repoURL: https://charts.example.com
      chart: extra
      targetRevision: 0.0.1
`
		require.NoError(t, os.WriteFile(filepath.Join(workDir, "apps", "demo.yaml"), []byte(mixed), 0o644))
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMixedMultiSource)
}
