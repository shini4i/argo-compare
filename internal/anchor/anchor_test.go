package anchor

import (
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_SameRepo(t *testing.T) {
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/repo/charts/foo/.argo-compare.yml", []byte(`
application:
  path: cluster-state/foo/foo.yaml
`), 0o644))

	got, err := Load(fs, "/repo/charts/foo/.argo-compare.yml")
	require.NoError(t, err)
	assert.Equal(t, "cluster-state/foo/foo.yaml", got.Application.Path)
	assert.Empty(t, got.Application.Repo, "same-repo anchor should leave Repo empty")
	assert.Empty(t, got.Application.Branch, "branch is optional")
}

func TestLoad_CrossRepo(t *testing.T) {
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/repo/manifests/foo/staging/.argo-compare.yml", []byte(`
application:
  repo: ssh://git@example.com/group/apps.git
  path: stage/foo.yaml
  branch: main
`), 0o644))

	got, err := Load(fs, "/repo/manifests/foo/staging/.argo-compare.yml")
	require.NoError(t, err)
	assert.Equal(t, "ssh://git@example.com/group/apps.git", got.Application.Repo)
	assert.Equal(t, "stage/foo.yaml", got.Application.Path)
	assert.Equal(t, "main", got.Application.Branch)
}

func TestLoad_MissingApplicationPath(t *testing.T) {
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/repo/a/.argo-compare.yml", []byte(`
application:
  repo: ssh://git@example.com/group/apps.git
  branch: main
`), 0o644))

	_, err := Load(fs, "/repo/a/.argo-compare.yml")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidAnchor)
	assert.Contains(t, err.Error(), "application.path")
}

func TestLoad_MissingApplicationBlock(t *testing.T) {
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/repo/a/.argo-compare.yml", []byte("# only a comment\n"), 0o644))

	_, err := Load(fs, "/repo/a/.argo-compare.yml")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidAnchor)
	assert.Contains(t, err.Error(), "application.path")
}

func TestLoad_MalformedYAML(t *testing.T) {
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/repo/a/.argo-compare.yml", []byte("application: : : not-yaml\n"), 0o644))

	_, err := Load(fs, "/repo/a/.argo-compare.yml")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidAnchor)
}

func TestLoad_UnknownTopLevelKey(t *testing.T) {
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/repo/a/.argo-compare.yml", []byte(`
application:
  path: x.yaml
extras:
  unsupported: true
`), 0o644))

	_, err := Load(fs, "/repo/a/.argo-compare.yml")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidAnchor)
	assert.Contains(t, err.Error(), "extras")
}

func TestLoad_UnknownApplicationKey(t *testing.T) {
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/repo/a/.argo-compare.yml", []byte(`
application:
  path: x.yaml
  commit: abc123
`), 0o644))

	_, err := Load(fs, "/repo/a/.argo-compare.yml")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidAnchor)
	assert.Contains(t, err.Error(), "commit")
}

func TestLoad_FileNotFound(t *testing.T) {
	fs := afero.NewMemMapFs()
	_, err := Load(fs, "/repo/missing/.argo-compare.yml")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInvalidAnchor, "read errors must stay distinct from invalid-anchor errors")
}

func TestLoad_EmptyApplicationPath(t *testing.T) {
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/repo/a/.argo-compare.yml", []byte(`
application:
  path: ""
`), 0o644))

	_, err := Load(fs, "/repo/a/.argo-compare.yml")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidAnchor)
	assert.Contains(t, err.Error(), "application.path")
}

func TestLoad_WhitespaceApplicationPath(t *testing.T) {
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/repo/a/.argo-compare.yml", []byte(`
application:
  path: "   "
`), 0o644))

	_, err := Load(fs, "/repo/a/.argo-compare.yml")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidAnchor)
	assert.Contains(t, err.Error(), "application.path")
}
