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
	testFile := filepath.Join("..", "..", "testdata", "dynamic", "deployment.yaml")
	backupFile := testFile + ".bak"

	original, err := os.ReadFile(testFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(backupFile, original, 0o644))
	t.Cleanup(func() {
		require.NoError(t, os.Rename(backupFile, testFile))
	})

	tmpDir := filepath.Dir(testFile)

	c := &Compare{
		Globber: utils.CustomGlobber{},
		TmpDir:  tmpDir,
	}

	require.NoError(t, c.stripHelmLabels())

	modified, err := os.ReadFile(testFile)
	require.NoError(t, err)

	expected := `# for testing purpose we need only limited fields
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

	assert.Equal(t, expected, string(modified))
}

func TestCompareProcessFiles(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "templates", "src")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))

	file1 := filepath.Join(srcDir, "test.yaml")
	file2 := filepath.Join(srcDir, "test-values.yaml")
	copyFile(t, filepath.Join("..", "..", "testdata", "test.yaml"), file1)
	copyFile(t, filepath.Join("..", "..", "testdata", "test-values.yaml"), file2)

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

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, data, 0o644))
}
