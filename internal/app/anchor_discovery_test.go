package app

import (
	"sort"
	"testing"

	"github.com/shini4i/argo-compare/internal/anchor"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const anchorFileName = ".argo-compare.yml"

func writeAnchor(t *testing.T, fs afero.Fs, dir, path string) {
	t.Helper()
	require.NoError(t, afero.WriteFile(fs, dir+"/"+anchorFileName, []byte("application:\n  path: "+path+"\n"), 0o644))
}

func TestDiscoverAnchors_SingleAnchor(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeAnchor(t, fs, "/repo/charts/foo", "cluster-state/foo.yaml")

	groups, err := DiscoverAnchors("/repo", []string{
		fooValuesYAML,
		fooChartYAML,
	}, fs, anchorFileName)
	require.NoError(t, err)
	require.Len(t, groups, 1)

	assert.Equal(t, "/repo/charts/foo", groups[0].Dir)
	assert.Equal(t, "cluster-state/foo.yaml", groups[0].Anchor.Application.Path)
	sort.Strings(groups[0].ChangedFiles)
	assert.Equal(t, []string{fooChartYAML, fooValuesYAML}, groups[0].ChangedFiles)
}

func TestDiscoverAnchors_MultipleAnchors(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeAnchor(t, fs, "/repo/charts/foo", "apps/foo.yaml")
	writeAnchor(t, fs, "/repo/charts/bar", "apps/bar.yaml")

	groups, err := DiscoverAnchors("/repo", []string{
		fooValuesYAML,
		"charts/bar/values.yaml",
		"charts/bar/templates/x.yaml",
	}, fs, anchorFileName)
	require.NoError(t, err)
	require.Len(t, groups, 2)

	byDir := map[string]AnchorGroup{}
	for _, g := range groups {
		byDir[g.Dir] = g
	}

	foo := byDir["/repo/charts/foo"]
	assert.Equal(t, "apps/foo.yaml", foo.Anchor.Application.Path)
	assert.Equal(t, []string{fooValuesYAML}, foo.ChangedFiles)

	bar := byDir["/repo/charts/bar"]
	sort.Strings(bar.ChangedFiles)
	assert.Equal(t, "apps/bar.yaml", bar.Anchor.Application.Path)
	assert.Equal(t, []string{"charts/bar/templates/x.yaml", "charts/bar/values.yaml"}, bar.ChangedFiles)
}

func TestDiscoverAnchors_NestedAnchorsPickNearest(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeAnchor(t, fs, "/repo/charts", "apps/outer.yaml")
	writeAnchor(t, fs, "/repo/charts/foo", "apps/foo.yaml")

	groups, err := DiscoverAnchors("/repo", []string{
		fooValuesYAML,
	}, fs, anchorFileName)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "/repo/charts/foo", groups[0].Dir, "should pick the nearest enclosing anchor")
}

func TestDiscoverAnchors_FilesWithNoEnclosingAnchor(t *testing.T) {
	fs := afero.NewMemMapFs()
	// No .argo-compare.yml anywhere.
	groups, err := DiscoverAnchors("/repo", []string{
		"apps/foo.yaml",
		"apps/bar.yaml",
	}, fs, anchorFileName)
	require.NoError(t, err)
	assert.Empty(t, groups, "files with no enclosing anchor produce no groups")
}

func TestDiscoverAnchors_MixedAnchoredAndUnanchored(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeAnchor(t, fs, "/repo/charts/foo", "apps/foo.yaml")

	groups, err := DiscoverAnchors("/repo", []string{
		fooValuesYAML,
		"apps/some-app.yaml", // not under any anchor
	}, fs, anchorFileName)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, []string{fooValuesYAML}, groups[0].ChangedFiles)
}

func TestDiscoverAnchors_AnchorFileItselfChanged(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeAnchor(t, fs, "/repo/charts/foo", "apps/foo.yaml")

	groups, err := DiscoverAnchors("/repo", []string{
		"charts/foo/.argo-compare.yml",
	}, fs, anchorFileName)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "/repo/charts/foo", groups[0].Dir)
	assert.Equal(t, []string{"charts/foo/.argo-compare.yml"}, groups[0].ChangedFiles)
}

func TestDiscoverAnchors_RepoRootAnchor(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeAnchor(t, fs, "/repo", "apps/root.yaml")

	groups, err := DiscoverAnchors("/repo", []string{
		"values.yaml",
		"nested/inner/file.yaml",
	}, fs, anchorFileName)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "/repo", groups[0].Dir)
}

func TestDiscoverAnchors_InvalidAnchorFile(t *testing.T) {
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/repo/charts/foo/.argo-compare.yml", []byte("not-yaml: : :"), 0o644))

	_, err := DiscoverAnchors("/repo", []string{
		fooValuesYAML,
	}, fs, anchorFileName)
	require.Error(t, err, "malformed .argo-compare.yml must be a hard error")
	assert.ErrorIs(t, err, anchor.ErrInvalidAnchor)
}

func TestDiscoverAnchors_EmptyChangedFiles(t *testing.T) {
	fs := afero.NewMemMapFs()
	groups, err := DiscoverAnchors("/repo", nil, fs, anchorFileName)
	require.NoError(t, err)
	assert.Empty(t, groups)
}
