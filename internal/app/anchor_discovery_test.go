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

	// The anchor file is a marker, not application content. Adding or editing
	// it on its own must not seed a group, otherwise bulk-onboarding hundreds
	// of `.argo-compare.yml` files would trigger comparisons with no real
	// changes.
	groups, err := DiscoverAnchors("/repo", []string{
		"charts/foo/.argo-compare.yml",
	}, fs, anchorFileName)
	require.NoError(t, err)
	assert.Empty(t, groups, "a marker-only change must not seed an anchor group")
}

func TestDiscoverAnchors_AnchorFileChangedAlongsideRealFile(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeAnchor(t, fs, "/repo/charts/foo", "apps/foo.yaml")

	// When real content changes alongside the marker, the group still forms
	// from the real files and the marker is excluded from ChangedFiles.
	groups, err := DiscoverAnchors("/repo", []string{
		"charts/foo/.argo-compare.yml",
		fooValuesYAML,
	}, fs, anchorFileName)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "/repo/charts/foo", groups[0].Dir)
	assert.Equal(t, []string{fooValuesYAML}, groups[0].ChangedFiles)
}

func TestDiscoverAnchors_RepoRootMarkerOnlyChanged(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeAnchor(t, fs, "/repo", "apps/root.yaml")

	// The repo root is the structural boundary where findAnchorDir stops
	// walking. The marker-skip guard must fire even here, so a marker-only
	// change at the root seeds no group.
	groups, err := DiscoverAnchors("/repo", []string{
		".argo-compare.yml",
	}, fs, anchorFileName)
	require.NoError(t, err)
	assert.Empty(t, groups, "a marker-only change at the repo root must not seed a group")
}

func TestDiscoverAnchors_NestedMarkersOnlyChanged(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeAnchor(t, fs, "/repo/charts", "apps/outer.yaml")
	writeAnchor(t, fs, "/repo/charts/foo", "apps/foo.yaml")

	// Both the outer and inner markers changed with no real content. Neither
	// may seed a group, regardless of the nearest-anchor walk.
	groups, err := DiscoverAnchors("/repo", []string{
		"charts/.argo-compare.yml",
		"charts/foo/.argo-compare.yml",
	}, fs, anchorFileName)
	require.NoError(t, err)
	assert.Empty(t, groups, "marker-only changes in a nested layout must not seed groups")
}

func TestDiscoverAnchors_MultipleAnchorsOneMarkerOnly(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeAnchor(t, fs, "/repo/charts/foo", "apps/foo.yaml")
	writeAnchor(t, fs, "/repo/charts/bar", "apps/bar.yaml")

	// foo's marker changed on its own (skipped); bar has real content. Only
	// bar forms a group — skipping the marker must not suppress unrelated ones.
	groups, err := DiscoverAnchors("/repo", []string{
		"charts/foo/.argo-compare.yml",
		"charts/bar/values.yaml",
	}, fs, anchorFileName)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "/repo/charts/bar", groups[0].Dir)
	assert.Equal(t, []string{"charts/bar/values.yaml"}, groups[0].ChangedFiles)
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
