package app

import (
	"os"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports/portstest"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestDirectoriesOf pins the shared foundation of both comparison legs. Input
// is slash-separated repo-relative file paths; callers do the conversion.
func TestDirectoriesOf(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  []string
	}{
		{name: "nil", files: nil, want: []string{}},
		{name: "file at the repository root implies no directory", files: []string{"README.md"}, want: []string{}},
		{name: "every ancestor is reported", files: []string{"a/b/c.yaml"}, want: []string{"a", "a/b"}},
		{
			name:  "shared ancestors appear once",
			files: []string{"a/b/one.yaml", "a/b/two.yaml", "a/c/three.yaml"},
			want:  []string{"a", "a/b", "a/c"},
		},
		{name: "empty path", files: []string{""}, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, directoriesOf(tt.files))
		})
	}
}

func gitGeneratorAppSet(t *testing.T, repoURL, revision string) *models.ApplicationSet {
	t.Helper()

	manifest := `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: ` + repoURL + `
        revision: ` + revision + `
        directories:
          - path: clusters/*
  template:
    metadata:
      name: '{{ .path.basename }}'
`

	var appSet models.ApplicationSet
	require.NoError(t, yaml.Unmarshal([]byte(manifest), &appSet))

	return &appSet
}

func TestAssertGitGeneratorsComparableAcceptsLocalRepository(t *testing.T) {
	const origin = "https://github.com/shini4i/argo-compare.git"

	tests := []struct {
		name     string
		repoURL  string
		revision string
	}{
		{name: "identical url", repoURL: origin, revision: "HEAD"},
		{name: "scp form of the same repository", repoURL: "git@github.com:shini4i/argo-compare.git", revision: "HEAD"},
		{name: "without the git suffix", repoURL: "https://github.com/shini4i/argo-compare", revision: "HEAD"},
		{name: "empty revision", repoURL: origin, revision: `""`},
		{name: "revision naming the compared branch", repoURL: origin, revision: "main"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appSet := gitGeneratorAppSet(t, tt.repoURL, tt.revision)
			assert.NoError(t, assertGitGeneratorsComparable(appSet, origin, "main"))
		})
	}
}

func TestAssertGitGeneratorsComparableRejects(t *testing.T) {
	const origin = "https://github.com/shini4i/argo-compare.git"

	tests := []struct {
		name       string
		repoURL    string
		revision   string
		wantErrMsg string
	}{
		{
			name:       "another repository",
			repoURL:    "https://github.com/someone/elsewhere.git",
			revision:   "HEAD",
			wantErrMsg: "elsewhere.git",
		},
		{
			name:       "revision pinned to a tag",
			repoURL:    origin,
			revision:   "v1.2.3",
			wantErrMsg: `revision "v1.2.3"`,
		},
		{
			name:       "revision naming an unrelated branch",
			repoURL:    origin,
			revision:   "release",
			wantErrMsg: `revision "release"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appSet := gitGeneratorAppSet(t, tt.repoURL, tt.revision)

			err := assertGitGeneratorsComparable(appSet, origin, "main")
			assert.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
			assert.Contains(t, err.Error(), tt.wantErrMsg)
		})
	}
}

// TestAssertGitGeneratorsComparableIgnoresOtherGenerators keeps the check from
// touching manifests that declare no git generator at all.
func TestAssertGitGeneratorsComparableIgnoresOtherGenerators(t *testing.T) {
	var appSet models.ApplicationSet
	require.NoError(t, yaml.Unmarshal([]byte(`
kind: ApplicationSet
metadata:
  name: guestbook
spec:
  goTemplate: true
  generators:
    - list:
        elements:
          - cluster: dev
  template:
    metadata:
      name: '{{ .cluster }}'
`), &appSet))

	assert.NoError(t, assertGitGeneratorsComparable(&appSet, "https://example.com/repo.git", "main"))
	assert.NoError(t, assertGitGeneratorsComparable(&models.ApplicationSet{}, "", "main"))
}

// TestGitTreeReadsCommittedDirectories proves the reader both legs share
// reports exactly the directories Git records, and nothing from the working
// tree that Git does not track.
func TestGitTreeReadsCommittedDirectories(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepoState(t,
		branchState{manifest: applicationSetYAML([][2]string{{"dev", "1.0.0"}}), files: map[string]string{
			"clusters/dev/values.yaml": "replicaCount: 1\n",
		}},
		branchState{manifest: applicationSetYAML([][2]string{{"dev", "1.0.0"}}), files: map[string]string{
			"clusters/dev/values.yaml":         "replicaCount: 1\n",
			"clusters/prod/addons/values.yaml": "replicaCount: 2\n",
		}})

	repoInstance, err := NewGitRepo(afero.NewOsFs(), portstest.NoopCmdRunner{}, utils.OsFileReader{}, logger.New("tree-lister-test"))
	require.NoError(t, err)

	headTree, err := repoInstance.HeadTree()
	require.NoError(t, err)
	headDirs, err := gitTree{tree: headTree}.Directories()
	require.NoError(t, err)
	assert.Equal(t, []string{"apps", "clusters", "clusters/dev", "clusters/prod", "clusters/prod/addons"}, headDirs)

	baseTree, err := repoInstance.MergeBaseTreeFor("main")
	require.NoError(t, err)
	baseDirs, err := gitTree{tree: baseTree}.Directories()
	require.NoError(t, err)
	assert.Equal(t, []string{"apps", "clusters", "clusters/dev"}, baseDirs)

	// An untracked directory must not reach either leg.
	require.NoError(t, os.MkdirAll("untracked/build", 0o755))
	require.NoError(t, os.WriteFile("untracked/build/out.yaml", []byte("x: 1\n"), 0o644))

	headDirs, err = gitTree{tree: headTree}.Directories()
	require.NoError(t, err)
	assert.NotContains(t, headDirs, "untracked")
	assert.NotContains(t, headDirs, "untracked/build")
}

// TestGitTreeReadsCommittedFiles covers the file half of the reader, which the
// file generator depends on: paths sorted and repo-relative, contents exact,
// and a missing path named in the error.
func TestGitTreeReadsCommittedFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	manifest := applicationSetYAML([][2]string{{"dev", "1.0.0"}})
	seedAppSetRepoState(t,
		branchState{manifest: manifest},
		branchState{manifest: manifest, files: map[string]string{
			"clusters/dev/config.yaml": "cluster:\n  name: dev\n",
		}})

	repoInstance, err := NewGitRepo(afero.NewOsFs(), portstest.NoopCmdRunner{}, utils.OsFileReader{}, logger.New("tree-files-test"))
	require.NoError(t, err)

	headTree, err := repoInstance.HeadTree()
	require.NoError(t, err)
	reader := gitTree{tree: headTree}

	files, err := reader.Files()
	require.NoError(t, err)
	assert.Equal(t, []string{"README.md", appSetPath, "clusters/dev/config.yaml"}, files)

	content, err := reader.ReadFile("clusters/dev/config.yaml")
	require.NoError(t, err)
	assert.Equal(t, "cluster:\n  name: dev\n", string(content))

	_, err = reader.ReadFile("clusters/missing.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "find clusters/missing.yaml in tree")
}
