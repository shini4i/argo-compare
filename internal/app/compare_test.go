package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	helmDeploymentWithManagedLabels = `# for testing purpose we need only limited fields
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app.kubernetes.io/instance: traefik-web
    app.kubernetes.io/managed-by: Helm
    app.kubernetes.io/name: traefik
    argocd.argoproj.io/instance: traefik
    helm.sh/chart: traefik-23.0.1
  name: traefik
  namespace: web
`
	expectedStrippedDeployment = `# for testing purpose we need only limited fields
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app.kubernetes.io/instance: traefik-web
    app.kubernetes.io/name: traefik
    argocd.argoproj.io/instance: traefik
  name: traefik
  namespace: web
`
	appManifestYAML = `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: ingress-nginx
  namespace: argo-cd
spec:
  source:
    repoURL: https://kubernetes.github.io/ingress-nginx
    chart: ingress-nginx
    targetRevision: "4.2.3"
    helm:
      values: |
        fullnameOverride: ingress-nginx
        controller:
          kind: DaemonSet
          service:
            externalTrafficPolicy: Local
            annotations:
              fancyAnnotation: false
`
	appValuesYAML = `fullnameOverride: ingress-nginx
controller:
  kind: DaemonSet
  service:
    externalTrafficPolicy: Local
    annotations:
      fancyAnnotation: false
`
)

func TestCompareGenerateFilesStatus(t *testing.T) {
	c := Compare{}

	c.srcFiles = []File{
		{Name: "file1", Sha: "1234"},
		{Name: "file3", Sha: "3456"},
		{Name: "file4", Sha: "7890"},
	}

	c.dstFiles = []File{
		{Name: "file1", Sha: "5678"},
		{Name: "file2", Sha: "9012"},
		{Name: "file3", Sha: "3456"},
	}

	c.generateFilesStatus()

	assert.Equal(t, []File{{Name: "file4", Sha: "7890"}}, c.addedFiles)
	assert.Equal(t, []File{{Name: "file2", Sha: "9012"}}, c.removedFiles)
	assert.Equal(t, []File{{Name: "file1", Sha: "1234"}}, c.diffFiles)
}

func TestCompareFindAndStripHelmLabels(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "deployment.yaml")
	require.NoError(t, os.WriteFile(testFile, []byte(helmDeploymentWithManagedLabels), 0o644))

	c := &Compare{
		Globber: utils.CustomGlobber{},
		TmpDir:  tmpDir,
	}

	require.NoError(t, c.stripHelmLabels())

	modified, err := os.ReadFile(testFile)
	require.NoError(t, err)

	assert.Equal(t, expectedStrippedDeployment, string(modified))
}

func TestCompareProcessFiles(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "templates", "src")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))

	file1 := filepath.Join(srcDir, "test.yaml")
	file2 := filepath.Join(srcDir, "test-values.yaml")
	require.NoError(t, os.WriteFile(file1, []byte(appManifestYAML), 0o644))
	require.NoError(t, os.WriteFile(file2, []byte(appValuesYAML), 0o644))

	c := &Compare{TmpDir: tmpDir}

	files := []string{file1, file2}
	found, err := c.processFiles(files, "src")
	require.NoError(t, err)

	assert.Len(t, found, 2)
	assert.Equal(t, strings.TrimPrefix(file1, filepath.Join(tmpDir, "templates", "src")), found[0].Name)
	assert.NotEmpty(t, found[0].Sha)
	assert.Equal(t, strings.TrimPrefix(file2, filepath.Join(tmpDir, "templates", "src")), found[1].Name)
	assert.NotEmpty(t, found[1].Sha)
}

func TestStdoutStrategyPresent(t *testing.T) {
	var buf bytes.Buffer
	backend := logging.NewLogBackend(&buf, "", 0)
	logging.SetBackend(logging.NewBackendFormatter(backend, logging.MustStringFormatter(`%{message}`)))
	t.Cleanup(func() {
		logging.SetBackend(logging.NewBackendFormatter(logging.NewLogBackend(os.Stdout, "", 0), logging.MustStringFormatter(`%{message}`)))
	})

	strategy := StdoutStrategy{
		Log:         logging.MustGetLogger("compare-print"),
		ShowAdded:   true,
		ShowRemoved: true,
	}

	result := ComparisonResult{}
	require.NoError(t, strategy.Present(result))
	assert.Contains(t, buf.String(), "No diff was found in rendered manifests!")

	buf.Reset()

	result = ComparisonResult{
		Added:   []DiffOutput{{File: File{Name: "file1"}, Diff: "diff-added"}},
		Removed: []DiffOutput{{File: File{Name: "file2"}, Diff: "diff-removed"}},
		Changed: []DiffOutput{{File: File{Name: "file3"}, Diff: "diff-changed"}},
	}

	require.NoError(t, strategy.Present(result))
	logs := buf.String()
	assert.Contains(t, logs, "The following 1 file would be added")
	assert.Contains(t, logs, "The following 1 file would be removed")
	assert.Contains(t, logs, "The following 1 file would be changed")
	assert.Contains(t, logs, "file1")
	assert.Contains(t, logs, "file2")
	assert.Contains(t, logs, "file3")
}

func TestCompareExecuteProducesDiffs(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "templates", "src")
	dstDir := filepath.Join(tmpDir, "templates", "dst")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	require.NoError(t, os.MkdirAll(dstDir, 0o755))

	write := func(dir, name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}

	write(srcDir, "added.yaml", "kind: ConfigMap\nmetadata:\n  name: added\n")
	write(dstDir, "removed.yaml", "kind: ConfigMap\nmetadata:\n  name: removed\n")
	write(srcDir, "changed.yaml", "kind: ConfigMap\nmetadata:\n  name: changed\n  labels:\n    side: src\n")
	write(dstDir, "changed.yaml", "kind: ConfigMap\nmetadata:\n  name: changed\n  labels:\n    side: dst\n")

	compare := Compare{
		Globber:            utils.CustomGlobber{},
		TmpDir:             tmpDir,
		PreserveHelmLabels: true,
	}

	result, err := compare.Execute()
	require.NoError(t, err)

	require.Len(t, result.Added, 1)
	assert.Equal(t, "/added.yaml", result.Added[0].File.Name)
	require.Len(t, result.Removed, 1)
	assert.Equal(t, "/removed.yaml", result.Removed[0].File.Name)
	require.Len(t, result.Changed, 1)
	assert.Equal(t, "/changed.yaml", result.Changed[0].File.Name)
	assert.Contains(t, result.Changed[0].Diff, "-    side: dst")
	assert.Contains(t, result.Changed[0].Diff, "+    side: src")
}
