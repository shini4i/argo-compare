package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commitTreeWith writes the given files (relative paths → contents) to a
// fresh git repo, commits them, and returns the resulting tree object.
func commitTreeWith(t *testing.T, files map[string]string) *object.Tree {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)

	for path, content := range files {
		abs := filepath.Join(dir, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}

	worktree, err := repo.Worktree()
	require.NoError(t, err)
	for path := range files {
		_, err := worktree.Add(path)
		require.NoError(t, err)
	}
	hash, err := worktree.Commit("seed", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)
	commit, err := repo.CommitObject(hash)
	require.NoError(t, err)
	tree, err := commit.Tree()
	require.NoError(t, err)
	return tree
}

func TestMaterializeTreeDir_FlatChart(t *testing.T) {
	tree := commitTreeWith(t, map[string]string{
		"charts/foo/Chart.yaml":  "name: foo\n",
		"charts/foo/values.yaml": "replicaCount: 1\n",
		"unrelated/other.yaml":   "ignored\n",
	})

	dest := t.TempDir()
	require.NoError(t, MaterializeTreeDir(context.Background(), afero.NewOsFs(), tree, "charts/foo", dest))

	chart, err := os.ReadFile(filepath.Join(dest, "Chart.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "name: foo\n", string(chart))

	values, err := os.ReadFile(filepath.Join(dest, "values.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "replicaCount: 1\n", string(values))

	_, err = os.Stat(filepath.Join(dest, "unrelated"))
	assert.True(t, os.IsNotExist(err), "files outside subpath must not be materialized")
}

func TestMaterializeTreeDir_NestedDirectories(t *testing.T) {
	tree := commitTreeWith(t, map[string]string{
		"charts/foo/Chart.yaml":          "name: foo\n",
		"charts/foo/values.yaml":         "replicaCount: 1\n",
		"charts/foo/templates/dep.yaml":  "kind: Deployment\n",
		"charts/foo/templates/svc.yaml":  "kind: Service\n",
		"charts/foo/charts/sub/x.yaml":   "sub-chart-file\n",
	})

	dest := t.TempDir()
	require.NoError(t, MaterializeTreeDir(context.Background(), afero.NewOsFs(), tree, "charts/foo", dest))

	for _, rel := range []string{"Chart.yaml", "values.yaml", "templates/dep.yaml", "templates/svc.yaml", "charts/sub/x.yaml"} {
		_, err := os.Stat(filepath.Join(dest, rel))
		assert.NoError(t, err, "expected %s to exist", rel)
	}
}

func TestMaterializeTreeDir_SubpathMissing(t *testing.T) {
	tree := commitTreeWith(t, map[string]string{
		"charts/foo/Chart.yaml": "name: foo\n",
	})

	dest := t.TempDir()
	err := MaterializeTreeDir(context.Background(), afero.NewOsFs(), tree, "charts/bar", dest)
	require.Error(t, err)
}

func TestMaterializeTreeDir_SubpathIsFile(t *testing.T) {
	tree := commitTreeWith(t, map[string]string{
		"charts/foo/Chart.yaml": "name: foo\n",
	})

	dest := t.TempDir()
	err := MaterializeTreeDir(context.Background(), afero.NewOsFs(), tree, "charts/foo/Chart.yaml", dest)
	require.Error(t, err, "subpath must be a directory inside the tree")
}

func TestMaterializeTreeDir_RootSubpath(t *testing.T) {
	tree := commitTreeWith(t, map[string]string{
		"Chart.yaml":  "name: top\n",
		"values.yaml": "replicaCount: 1\n",
	})

	dest := t.TempDir()
	require.NoError(t, MaterializeTreeDir(context.Background(), afero.NewOsFs(), tree, ".", dest))

	chart, err := os.ReadFile(filepath.Join(dest, "Chart.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "name: top\n", string(chart))
}

func TestMaterializeTreeDir_ContextCancelled(t *testing.T) {
	tree := commitTreeWith(t, map[string]string{
		"charts/foo/Chart.yaml": "name: foo\n",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dest := t.TempDir()
	err := MaterializeTreeDir(ctx, afero.NewOsFs(), tree, "charts/foo", dest)
	require.Error(t, err)
}
