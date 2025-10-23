package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/spf13/afero"
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
	testBackend := logging.NewLogBackend(&logBuffer, "", 0)
	logging.SetBackend(logging.NewBackendFormatter(testBackend, logging.MustStringFormatter(`%{message}`)))
	t.Cleanup(func() {
		logging.SetBackend(logging.NewBackendFormatter(logging.NewLogBackend(os.Stdout, "", 0), logging.MustStringFormatter(`%{message}`)))
	})

	logger := logging.MustGetLogger("app-test")

	helmStub := newStubHelmProcessor(t)
	cmdStub := &stubCmdRunner{}

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
		CmdRunner:     cmdStub,
		FileReader:    utils.OsFileReader{},
		HelmProcessor: helmStub,
		Globber:       utils.CustomGlobber{},
		Logger:        logger,
	})
	require.NoError(t, err)

	err = appInstance.Run()
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

	appPath := filepath.Join(repoDir, "apps")
	require.NoError(t, os.MkdirAll(appPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(appPath, "demo.yaml"), content, 0o644))
}

func defaultSignature() *object.Signature {
	return &object.Signature{
		Name:  "CI Bot",
		Email: "ci@example.com",
		When:  time.Now(),
	}
}

type stubCmdRunner struct{}

func (s *stubCmdRunner) Run(string, ...string) (string, string, error) {
	return "", "", nil
}

type stubHelmProcessor struct {
	t       *testing.T
	mu      sync.Mutex
	calls   map[string]int
	tmpDirs map[string]struct{}
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

func (s *stubHelmProcessor) GenerateValuesFile(chartName, tmpDir, targetType, values string, valuesObject map[string]interface{}) error {
	s.record("GenerateValuesFile", tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}
	if values == "" && valuesObject == nil {
		values = "replicaCount: 1\n"
	}
	path := filepath.Join(tmpDir, fmt.Sprintf("%s-values-%s.yaml", chartName, targetType))
	return os.WriteFile(path, []byte(values), 0o600)
}

func (s *stubHelmProcessor) DownloadHelmChart(_ ports.CmdRunner, _ ports.Globber, _ string, _ string, _ string, _ string, _ []models.RepoCredentials) error {
	s.record("DownloadHelmChart", "")
	return nil
}

func (s *stubHelmProcessor) ExtractHelmChart(_ ports.CmdRunner, _ ports.Globber, chartName, chartVersion, _ string, tmpDir, targetType string) error {
	s.record("ExtractHelmChart", tmpDir)
	dir := filepath.Join(tmpDir, "charts", targetType, chartName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf("chartVersion: %s\n", chartVersion)
	return os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(content), 0o644)
}

func (s *stubHelmProcessor) RenderAppSource(_ ports.CmdRunner, releaseName, chartName, chartVersion, tmpDir, targetType, namespace string) error {
	s.record("RenderAppSource", tmpDir)
	dir := filepath.Join(tmpDir, "templates", targetType, chartName)
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
`, releaseName, namespace, chartVersion)
	return os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o644)
}
