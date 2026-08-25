package app

import (
	"errors"
	"path"
	"strings"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTouchedDirectories(t *testing.T) {
	// Every ancestor is reported, since a generator may match any level.
	assert.Equal(t,
		[]string{"clusters", "clusters/dev", "clusters/dev/addons"},
		touchedDirectories([]string{"clusters/dev/addons/values.yaml"}))

	assert.Equal(t,
		[]string{"apps", "clusters", "clusters/dev"},
		touchedDirectories([]string{"clusters/dev/values.yaml", "apps/x.yaml", "apps/y.yaml"}))

	// A file at the repository root touches no directory.
	assert.Empty(t, touchedDirectories([]string{"README.md"}))
	assert.Empty(t, touchedDirectories([]string{""}))
	assert.Empty(t, touchedDirectories(nil))
}

func TestMergePaths(t *testing.T) {
	assert.Equal(t,
		[]string{"a.yaml", "b.yaml", "c.yaml"},
		mergePaths([]string{"a.yaml", "b.yaml"}, []string{"b.yaml", "c.yaml"}))

	assert.Equal(t, []string{"a.yaml"}, mergePaths([]string{"a.yaml"}, nil))
	assert.Equal(t, []string{"c.yaml"}, mergePaths(nil, []string{"c.yaml", "c.yaml"}))
}

// discoveryOrigin is the repository the seeded manifests name.
const discoveryOrigin = "https://example.com/repo.git"

const discoverableAppSet = `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: cluster-addons
  namespace: argocd
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://example.com/repo.git
        directories:
          - path: clusters/*
          - path: clusters/donotdeploy
            exclude: true
  template:
    metadata:
      name: '{{ .path.basename }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: demo
        targetRevision: 1.0.0
`

// seedDiscoveryTree writes the manifests a scan should find, alongside files it
// must ignore: a list-generator ApplicationSet, a plain Application, an
// unparseable manifest, and one inside a dot directory.
func seedDiscoveryTree(t *testing.T) (afero.Fs, string) {
	t.Helper()

	root := t.TempDir()
	filesystem := afero.NewOsFs()

	write := func(rel, content string) {
		t.Helper()
		require.NoError(t, filesystem.MkdirAll(root+"/"+path.Dir(rel), 0o755))
		require.NoError(t, afero.WriteFile(filesystem, root+"/"+rel, []byte(content), 0o644))
	}

	write("apps/addons.yaml", discoverableAppSet)
	write("apps/list.yaml", `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: listed
spec:
  goTemplate: true
  generators:
    - list:
        elements:
          - cluster: dev
  template:
    metadata:
      name: '{{ .cluster }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: demo
        targetRevision: 1.0.0
`)
	write("apps/plain.yaml", "kind: Application\nmetadata:\n  name: demo\n")
	write("apps/broken.yaml", "kind: ApplicationSet\nmetadata:\n   bad: indent\n  worse\n")
	write("apps/notes.txt", "kind: ApplicationSet\n")
	write(".hidden/addons.yaml", discoverableAppSet)

	return filesystem, root
}

// TestDiscoverApplicationSetsMatchesTouchedDirectory is the case the feature
// exists for: a change under clusters/ reaches an ApplicationSet nobody edited.
func TestDiscoverApplicationSetsMatchesTouchedDirectory(t *testing.T) {
	filesystem, root := seedDiscoveryTree(t)

	matched, _, err := DiscoverApplicationSets(filesystem, utils.OsFileReader{}, root, discoveryOrigin, "main",
		[]string{"clusters/staging/values.yaml"})

	require.NoError(t, err)
	assert.Equal(t, []string{"apps/addons.yaml"}, matched)
}

func TestDiscoverApplicationSetsIgnoresUnmatchedChanges(t *testing.T) {
	filesystem, root := seedDiscoveryTree(t)
	reader := utils.OsFileReader{}

	// Excluded by the generator itself.
	matched, _, err := DiscoverApplicationSets(filesystem, reader, root, discoveryOrigin, "main", []string{"clusters/donotdeploy/values.yaml"})
	require.NoError(t, err)
	assert.Empty(t, matched)

	// Outside every pattern.
	matched, _, err = DiscoverApplicationSets(filesystem, reader, root, discoveryOrigin, "main", []string{"docs/readme.yaml"})
	require.NoError(t, err)
	assert.Empty(t, matched)

	// A repository-root file touches no directory at all.
	matched, _, err = DiscoverApplicationSets(filesystem, reader, root, discoveryOrigin, "main", []string{"README.md"})
	require.NoError(t, err)
	assert.Empty(t, matched)
}

// TestDiscoverApplicationSetsSkipsNonCandidates proves the scan tolerates the
// other manifests a repository holds without failing or matching them.
func TestDiscoverApplicationSetsSkipsNonCandidates(t *testing.T) {
	filesystem, root := seedDiscoveryTree(t)

	matched, _, err := DiscoverApplicationSets(filesystem, utils.OsFileReader{}, root, discoveryOrigin, "main",
		[]string{"clusters/dev/values.yaml"})

	require.NoError(t, err)
	// The list-generator, plain, broken, non-YAML and hidden manifests are all absent.
	assert.Equal(t, []string{"apps/addons.yaml"}, matched)
}

// failingReader fails on one path and delegates the rest, standing in for a
// manifest the scan cannot read. chmod would be a no-op when tests run as root.
type failingReader struct {
	failPath string
}

func (r failingReader) ReadFile(file string) ([]byte, error) {
	if strings.HasSuffix(file, r.failPath) {
		return nil, errors.New("boom")
	}
	return utils.OsFileReader{}.ReadFile(file)
}

// TestDiscoverApplicationSetsReportsUnusableManifests keeps a manifest that
// names the kind but cannot be loaded from vanishing without a word — it may be
// exactly the one the change affects.
func TestDiscoverApplicationSetsReportsUnusableManifests(t *testing.T) {
	filesystem, root := seedDiscoveryTree(t)

	matched, unusable, err := DiscoverApplicationSets(filesystem, failingReader{failPath: "apps/addons.yaml"},
		root, discoveryOrigin, "main", []string{"clusters/dev/values.yaml"})

	require.NoError(t, err)
	assert.Empty(t, matched, "the unreadable manifest cannot be matched")

	// The seeded tree also holds an unparseable manifest, so both are reported.
	joined := strings.Join(unusable, "\n")
	assert.Contains(t, joined, "apps/addons.yaml")
	assert.Contains(t, joined, "boom")
	assert.Contains(t, joined, "apps/broken.yaml")
}

// TestDiscoverApplicationSetsReportsUnparseableManifests covers the other half:
// the file reads, names the kind, and fails to decode.
func TestDiscoverApplicationSetsReportsUnparseableManifests(t *testing.T) {
	filesystem, root := seedDiscoveryTree(t)

	_, unusable, err := DiscoverApplicationSets(filesystem, utils.OsFileReader{},
		root, discoveryOrigin, "main", []string{"clusters/dev/values.yaml"})

	require.NoError(t, err)
	require.Len(t, unusable, 1)
	assert.Contains(t, unusable[0], "apps/broken.yaml")
	assert.Contains(t, unusable[0], "did not find expected key")
}
