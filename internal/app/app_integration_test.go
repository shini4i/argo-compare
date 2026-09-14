package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/shini4i/argo-compare/internal/ports/portstest"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppRunIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache")
	tmpBase := filepath.Join(tempDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpBase, 0o755))

	remoteDir := filepath.Join(tempDir, "origin.git")
	_, err := git.PlainInit(remoteDir, true)
	require.NoError(t, err)

	workDir := filepath.Join(tempDir, "work")
	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)

	err = repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main")))
	require.NoError(t, err)

	writeApplication(t, workDir, `1.0.0`, 1)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	_, err = worktree.Add("apps/demo.yaml")
	require.NoError(t, err)

	initialHash, err := worktree.Commit("initial commit", &git.CommitOptions{
		Author: defaultSignature(),
	})
	require.NoError(t, err)

	_, err = repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteDir},
	})
	require.NoError(t, err)

	err = repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{"refs/heads/main:refs/heads/main"},
	})
	require.NoError(t, err)

	err = repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), initialHash))
	require.NoError(t, err)

	err = worktree.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature/smoke"),
		Create: true,
	})
	require.NoError(t, err)

	writeApplication(t, workDir, `1.1.0`, 2)

	_, err = worktree.Add("apps/demo.yaml")
	require.NoError(t, err)

	_, err = worktree.Commit("update chart version", &git.CommitOptions{
		Author: defaultSignature(),
	})
	require.NoError(t, err)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldWD))
	})

	var logBuffer bytes.Buffer
	logger.RedirectForTest(t, &logBuffer)

	appLogger := logger.New("app-test")

	helmStub := newStubHelmProcessor(t)

	cfg := Config{
		TargetBranch:          "main",
		CacheDir:              cacheDir,
		TempDirBase:           tmpBase,
		PrintAddedManifests:   true,
		PrintRemovedManifests: true,
		Version:               "test",
	}

	appInstance, err := New(cfg, Dependencies{
		FS:            afero.NewOsFs(),
		CmdRunner:     portstest.NoopCmdRunner{},
		FileReader:    utils.OsFileReader{},
		HelmProcessor: helmStub,
		Globber:       utils.CustomGlobber{},
		Logger:        appLogger,
	})
	require.NoError(t, err)

	err = appInstance.Run(context.Background())
	require.NoError(t, err)

	require.Equal(t, 2, helmStub.callCount("RenderAppSource"))
	require.Equal(t, 2, helmStub.callCount("GenerateValuesFile"))
	require.Equal(t, 2, helmStub.callCount("ExtractHelmChart"))
	require.Equal(t, 2, helmStub.callCount("DownloadHelmChart"))

	for dir := range helmStub.tmpDirs {
		_, statErr := os.Stat(dir)
		require.Error(t, statErr)
		require.True(t, os.IsNotExist(statErr))
	}

	require.Contains(t, logBuffer.String(), "would be changed")
}

func writeApplication(t *testing.T, repoDir, version string, replicas int) {
	t.Helper()

	writeApplicationAt(t, repoDir, "apps/demo.yaml", version, replicas)
}

// writeApplicationAt writes the demo Application to relPath, a repo-relative
// slash-separated path, creating the parent directories as needed.
func writeApplicationAt(t *testing.T, repoDir, relPath, version string, replicas int) {
	t.Helper()

	content := []byte(`apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: demo
  namespace: argocd
spec:
  destination:
    server: https://kubernetes.default.svc
    namespace: demo
  source:
    repoURL: fake.repo/charts
    chart: demo-chart
    targetRevision: ` + version + `
    helm:
      releaseName: demo
      values: |
        replicaCount: ` + fmt.Sprintf("%d", replicas) + `
`)

	appFile := filepath.Join(repoDir, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(appFile), 0o755))
	require.NoError(t, os.WriteFile(appFile, content, 0o644))
}

func defaultSignature() *object.Signature {
	return &object.Signature{
		Name:  "CI Bot",
		Email: "ci@example.com",
		When:  time.Now(),
	}
}

type stubHelmProcessor struct {
	t       *testing.T
	mu      sync.Mutex
	calls   map[string]int
	tmpDirs map[string]struct{}
	// renderErrFor fails RenderAppSource for a given release name, so a test can
	// make one Application of a set fail while the rest render.
	renderErrFor map[string]error
	// renders captures what each render was handed, read at call time because
	// the workspace is deleted when the run ends.
	renders []stubRender
}

// stubRender is one RenderAppSource call: which leg it was for, and the values
// files it resolved to along with their contents.
type stubRender struct {
	TargetType  string
	ReleaseName string
	ValueFiles  map[string]string
}

func newStubHelmProcessor(t *testing.T) *stubHelmProcessor {
	t.Helper()
	return &stubHelmProcessor{
		t:       t,
		calls:   make(map[string]int),
		tmpDirs: make(map[string]struct{}),
	}
}

func (s *stubHelmProcessor) record(call, tmpDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[call]++
	if tmpDir != "" {
		s.tmpDirs[tmpDir] = struct{}{}
	}
}

func (s *stubHelmProcessor) callCount(call string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[call]
}

func (s *stubHelmProcessor) GenerateValuesFile(path, values string, valuesObject map[string]interface{}) error {
	s.record("GenerateValuesFile", filepath.Dir(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if values == "" && valuesObject == nil {
		values = "replicaCount: 1\n"
	}
	return os.WriteFile(path, []byte(values), 0o600)
}

func (s *stubHelmProcessor) DownloadHelmChart(_ context.Context, _ ports.HelmDeps, _ ports.ChartDownloadRequest) error {
	s.record("DownloadHelmChart", "")
	return nil
}

func (s *stubHelmProcessor) ExtractHelmChart(_ context.Context, _ ports.HelmDeps, req ports.ChartExtractRequest) error {
	s.record("ExtractHelmChart", req.ExtractDir)
	// Mirrors tar: the tarball's own top-level directory appears under ExtractDir.
	dir := filepath.Join(req.ExtractDir, req.ChartName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf("chartVersion: %s\n", req.ChartVersion)
	return os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(content), 0o644)
}

func (s *stubHelmProcessor) RenderAppSource(_ context.Context, _ ports.CmdRunner, req ports.ChartRenderRequest) error {
	s.record("RenderAppSource", req.OutputDir)
	s.recordRender(req)
	if err := s.renderErrFor[req.ReleaseName]; err != nil {
		return err
	}
	// helm reads the chart from ChartDir, so a layout where extraction and
	// rendering disagree must fail here rather than render from nothing.
	if _, err := os.Stat(req.ChartDir); err != nil {
		return fmt.Errorf("chart directory %q was never materialized: %w", req.ChartDir, err)
	}
	// Mirrors helm --output-dir, which nests the release and chart beneath it.
	dir := filepath.Join(req.OutputDir, req.ReleaseName, req.ChartName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	manifest := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  version: %s
`, req.ReleaseName, req.Namespace, req.ChartVersion)
	return os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o644)
}

func (s *stubHelmProcessor) BuildChartDependencies(_ context.Context, _ ports.HelmDeps, chartDir, _ string) error {
	s.record("BuildChartDependencies", chartDir)
	return nil
}

// recordRender stores the values files a render resolved to, reading each one
// so its content outlives the workspace.
func (s *stubHelmProcessor) recordRender(req ports.ChartRenderRequest) {
	files := make(map[string]string, len(req.ValueFiles))
	for _, vf := range req.ValueFiles {
		content, err := os.ReadFile(vf)
		if err != nil {
			files[vf] = ""
			continue
		}
		files[vf] = string(content)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renders = append(s.renders, stubRender{TargetType: req.TargetType, ReleaseName: req.ReleaseName, ValueFiles: files})
}

// renderFor returns the render recorded for one leg of one release.
func (s *stubHelmProcessor) renderFor(targetType, releaseName string) (stubRender, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.renders {
		if r.TargetType == targetType && r.ReleaseName == releaseName {
			return r, true
		}
	}
	return stubRender{}, false
}

// stubValidator returns the configured result on every call, regardless of inputs.
// Used to drive Run() through the success / failure paths in integration tests.
type stubValidator struct {
	result ports.ValidationResult
	err    error
	calls  int
	mu     sync.Mutex
}

func (s *stubValidator) Validate(_ context.Context, target, _ string) (ports.ValidationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	r := s.result
	r.Target = target
	return r, s.err
}

func TestAppRunReturnsValidationErrorWhenValidatorFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache")
	tmpBase := filepath.Join(tempDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpBase, 0o755))

	remoteDir := filepath.Join(tempDir, "origin.git")
	_, err := git.PlainInit(remoteDir, true)
	require.NoError(t, err)

	workDir := filepath.Join(tempDir, "work")
	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	writeApplication(t, workDir, `1.0.0`, 1)

	worktree, err := repo.Worktree()
	require.NoError(t, err)
	_, err = worktree.Add("apps/demo.yaml")
	require.NoError(t, err)
	initialHash, err := worktree.Commit("initial commit", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remoteDir}})
	require.NoError(t, err)
	require.NoError(t, repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{"refs/heads/main:refs/heads/main"},
	}))
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), initialHash)))

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature/smoke"),
		Create: true,
	}))
	writeApplication(t, workDir, `1.1.0`, 2)
	_, err = worktree.Add("apps/demo.yaml")
	require.NoError(t, err)
	_, err = worktree.Commit("update chart version", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWD)) })

	var logBuffer bytes.Buffer
	logger.RedirectForTest(t, &logBuffer)

	appLogger := logger.New("app-test-validation-fail")

	validator := &stubValidator{
		result: ports.ValidationResult{
			Valid:         false,
			ResourceCount: 1,
			ErrorCount:    1,
			Errors: []ports.ValidationError{
				{Kind: "ConfigMap", Name: "demo", Message: "schema mismatch (test)"},
			},
		},
	}

	cfg := Config{
		TargetBranch:      "main",
		CacheDir:          cacheDir,
		TempDirBase:       tmpBase,
		Version:           "test",
		ValidateManifests: true,
	}

	appInstance, err := New(cfg, Dependencies{
		FS:                afero.NewOsFs(),
		CmdRunner:         portstest.NoopCmdRunner{},
		FileReader:        utils.OsFileReader{},
		HelmProcessor:     newStubHelmProcessor(t),
		Globber:           utils.CustomGlobber{},
		Logger:            appLogger,
		ManifestValidator: validator,
	})
	require.NoError(t, err)

	err = appInstance.Run(context.Background())
	require.Error(t, err, "Run must return an error when validation fails")
	require.True(t, errors.Is(err, ErrManifestValidationFailed), "error must wrap ErrManifestValidationFailed, got: %v", err)

	// Validator was actually invoked (sanity check the test exercises the path).
	assert.Equal(t, 1, validator.calls, "validator must be called exactly once per application (src only)")
	// Diff still ran end-to-end before the failure was returned.
	assert.Contains(t, logBuffer.String(), "Manifest Validation Results")
}

func TestAppRunSucceedsWhenValidatorReportsValid(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache")
	tmpBase := filepath.Join(tempDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpBase, 0o755))

	remoteDir := filepath.Join(tempDir, "origin.git")
	_, err := git.PlainInit(remoteDir, true)
	require.NoError(t, err)

	workDir := filepath.Join(tempDir, "work")
	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	writeApplication(t, workDir, `1.0.0`, 1)
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	_, err = worktree.Add("apps/demo.yaml")
	require.NoError(t, err)
	initialHash, err := worktree.Commit("initial commit", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remoteDir}})
	require.NoError(t, err)
	require.NoError(t, repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{"refs/heads/main:refs/heads/main"},
	}))
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), initialHash)))

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature/smoke"),
		Create: true,
	}))
	writeApplication(t, workDir, `1.1.0`, 2)
	_, err = worktree.Add("apps/demo.yaml")
	require.NoError(t, err)
	_, err = worktree.Commit("update chart version", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWD)) })

	appLogger := logger.New("app-test-validation-ok")

	validator := &stubValidator{
		result: ports.ValidationResult{Valid: true, ResourceCount: 1},
	}

	cfg := Config{
		TargetBranch:      "main",
		CacheDir:          cacheDir,
		TempDirBase:       tmpBase,
		Version:           "test",
		ValidateManifests: true,
	}

	appInstance, err := New(cfg, Dependencies{
		FS:                afero.NewOsFs(),
		CmdRunner:         portstest.NoopCmdRunner{},
		FileReader:        utils.OsFileReader{},
		HelmProcessor:     newStubHelmProcessor(t),
		Globber:           utils.CustomGlobber{},
		Logger:            appLogger,
		ManifestValidator: validator,
	})
	require.NoError(t, err)

	err = appInstance.Run(context.Background())
	require.NoError(t, err, "Run must not return an error when validation passes")
	assert.Equal(t, 1, validator.calls, "validator must be called exactly once per application (src only)")
}
