package appset

import (
	"testing"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func include(path string) models.GitDirectory {
	return models.GitDirectory{Path: path}
}

func exclude(path string) models.GitDirectory {
	return models.GitDirectory{Path: path, Exclude: true}
}

func TestMatchDirectories(t *testing.T) {
	dirs := []string{
		"apps",
		"apps/guestbook",
		"apps/kustomize",
		"clusters",
		"clusters/dev",
		"clusters/prod",
		"clusters/prod/addons",
		".github",
		".github/workflows",
	}

	tests := []struct {
		name    string
		entries []models.GitDirectory
		want    []string
	}{
		{
			name:    "single level wildcard",
			entries: []models.GitDirectory{include("clusters/*")},
			want:    []string{"clusters/dev", "clusters/prod"},
		},
		{
			name:    "root wildcard matches top level only",
			entries: []models.GitDirectory{include("*")},
			want:    []string{"apps", "clusters"},
		},
		{
			name:    "exact path",
			entries: []models.GitDirectory{include("apps/guestbook")},
			want:    []string{"apps/guestbook"},
		},
		{
			name:    "exclude beats include",
			entries: []models.GitDirectory{include("clusters/*"), exclude("clusters/prod")},
			want:    []string{"clusters/dev"},
		},
		{
			name:    "exclude by pattern",
			entries: []models.GitDirectory{include("apps/*"), exclude("apps/k*")},
			want:    []string{"apps/guestbook"},
		},
		{
			name:    "exclude wins regardless of order",
			entries: []models.GitDirectory{exclude("clusters/prod"), include("clusters/*")},
			want:    []string{"clusters/dev"},
		},
		{
			name:    "several includes are unioned and stay sorted",
			entries: []models.GitDirectory{include("apps/*"), include("clusters/dev")},
			want:    []string{"apps/guestbook", "apps/kustomize", "clusters/dev"},
		},
		{
			name:    "no match yields nothing",
			entries: []models.GitDirectory{include("missing/*")},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, matchDirectories(dirs, tt.entries))
		})
	}
}

// TestMatchDirectoriesSkipsDotDirectories pins ArgoCD's behaviour: a directory
// whose name starts with a dot never generates an Application, so `.git` and
// `.github` cannot become one.
func TestMatchDirectoriesSkipsDotDirectories(t *testing.T) {
	dirs := []string{".github", ".github/workflows", "apps", ".git", ".git/refs"}

	// A root wildcard sees only the ordinary directory.
	assert.Equal(t, []string{"apps"}, matchDirectories(dirs, []models.GitDirectory{include("*")}))

	// Naming a dot directory outright still does not select it.
	assert.Empty(t, matchDirectories(dirs, []models.GitDirectory{include(".github")}))
	assert.Empty(t, matchDirectories(dirs, []models.GitDirectory{include(".github/*")}))
	assert.Empty(t, matchDirectories(dirs, []models.GitDirectory{include(".git/refs")}))
}

func TestDirectoryParams(t *testing.T) {
	params := directoryParams("clusters/eu-west-1/Prod_Cluster")

	path, ok := params["path"].(map[string]any)
	require.True(t, ok, "path must be a nested map for goTemplate access")

	assert.Equal(t, "clusters/eu-west-1/Prod_Cluster", path["path"])
	assert.Equal(t, []string{"clusters", "eu-west-1", "Prod_Cluster"}, path["segments"])
	assert.Equal(t, "Prod_Cluster", path["basename"])
	assert.Equal(t, "prod-cluster", path["basenameNormalized"])
}

func TestDirectoryParamsAtRepositoryRoot(t *testing.T) {
	params := directoryParams("guestbook")

	path, ok := params["path"].(map[string]any)
	require.True(t, ok, "path must be a nested map for goTemplate access")
	assert.Equal(t, "guestbook", path["path"])
	assert.Equal(t, []string{"guestbook"}, path["segments"])
	assert.Equal(t, "guestbook", path["basename"])
}

const gitAppSet = `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - git:
        repoURL: https://github.com/shini4i/argo-compare.git
        revision: HEAD
        directories:
          - path: clusters/*
          - path: clusters/donotdeploy
            exclude: true
  template:
    metadata:
      name: '{{ .path.basenameNormalized }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
        helm:
          valueFiles:
            - '{{ .path.path }}/values.yaml'
`

func TestExpandGitDirectoryGenerator(t *testing.T) {
	appSet := mustParse(t, gitAppSet)

	apps, err := Expand(appSet, dirTree("clusters", "clusters/dev", "clusters/prod", "clusters/donotdeploy", "apps/other"))
	require.NoError(t, err)
	require.Len(t, apps, 2)

	assert.Equal(t, "dev", apps[0].Metadata.Name)
	assert.Equal(t, []string{"clusters/dev/values.yaml"}, apps[0].Spec.Source.Helm.ValueFiles)
	assert.Equal(t, "prod", apps[1].Metadata.Name)
}

// TestExpandListsDirectoriesOnceForSeveralGitGenerators keeps a manifest with
// two git generators from walking the tree twice; both read the same tree.
func TestExpandListsDirectoriesOnceForSeveralGitGenerators(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://example.com/repo.git
        directories:
          - path: apps/*
    - git:
        repoURL: https://example.com/repo.git
        directories:
          - path: clusters/*
  template:
    metadata:
      name: '{{ .path.basename }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	tree := dirTree("apps/one", "clusters/two")
	apps, err := Expand(appSet, tree)

	require.NoError(t, err)
	assert.Equal(t, 1, tree.dirCalls)
	require.Len(t, apps, 2)
	assert.Equal(t, "one", apps[0].Metadata.Name)
	assert.Equal(t, "two", apps[1].Metadata.Name)
}

func TestExpandRequiresAListerForGitGenerators(t *testing.T) {
	appSet := mustParse(t, gitAppSet)

	_, err := Expand(appSet, nil)
	assert.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
}

func TestExpandPropagatesListerErrors(t *testing.T) {
	appSet := mustParse(t, gitAppSet)

	_, err := Expand(appSet, &fakeTree{dirErr: assert.AnError})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list directories for git generator")
}

// TestExpandDoesNotListForListGenerators keeps a list-only manifest from
// paying for a tree walk it has no use for.
func TestExpandDoesNotListForListGenerators(t *testing.T) {
	appSet := mustParse(t, `
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
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	tree := &fakeTree{}
	_, err := Expand(appSet, tree)

	require.NoError(t, err)
	assert.Zero(t, tree.dirCalls)
	assert.Zero(t, tree.fileCalls)
}

// TestMatches covers the gate that decides whether a directory change reaches
// the comparison at all. A regression here drops ApplicationSets silently.
func TestMatches(t *testing.T) {
	tests := []struct {
		name   string
		gitGen *models.GitGenerator
		dir    string
		want   bool
	}{
		{name: "nil generator", gitGen: nil, dir: "clusters/dev"},
		{name: "no entries", gitGen: &models.GitGenerator{}, dir: "clusters/dev"},
		{
			name:   "wildcard matches one level",
			gitGen: &models.GitGenerator{Directories: []models.GitDirectory{include("clusters/*")}},
			dir:    "clusters/dev",
			want:   true,
		},
		{
			name:   "wildcard does not cross a separator",
			gitGen: &models.GitGenerator{Directories: []models.GitDirectory{include("clusters/*")}},
			dir:    "clusters/prod/addons",
		},
		{
			name:   "exact path with no wildcard",
			gitGen: &models.GitGenerator{Directories: []models.GitDirectory{include("apps/guestbook")}},
			dir:    "apps/guestbook",
			want:   true,
		},
		{
			name:   "exact path does not match a child",
			gitGen: &models.GitGenerator{Directories: []models.GitDirectory{include("apps/guestbook")}},
			dir:    "apps/guestbook/sub",
		},
		{
			name:   "exclude wins",
			gitGen: &models.GitGenerator{Directories: []models.GitDirectory{include("clusters/*"), exclude("clusters/prod")}},
			dir:    "clusters/prod",
		},
		{
			name:   "exclude wins regardless of order",
			gitGen: &models.GitGenerator{Directories: []models.GitDirectory{exclude("clusters/prod"), include("clusters/*")}},
			dir:    "clusters/prod",
		},
		{
			name:   "exclude only never matches",
			gitGen: &models.GitGenerator{Directories: []models.GitDirectory{exclude("clusters/prod")}},
			dir:    "clusters/dev",
		},
		{
			name:   "dot segment is never generated",
			gitGen: &models.GitGenerator{Directories: []models.GitDirectory{include(".github")}},
			dir:    ".github",
		},
		{
			name:   "dot segment anywhere in the path",
			gitGen: &models.GitGenerator{Directories: []models.GitDirectory{include("apps/*/x")}},
			dir:    "apps/.hidden/x",
		},
		{
			name:   "empty directory",
			gitGen: &models.GitGenerator{Directories: []models.GitDirectory{include("*")}},
			dir:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Matches(tt.gitGen, tt.dir))
		})
	}
}

// TestMatchDirectoriesPatternShapes pins the shapes users most often get wrong.
func TestMatchDirectoriesPatternShapes(t *testing.T) {
	dirs := []string{"apps", "apps/guestbook", "apps/kustomize", "clusters", "clusters/dev", "clusters/prod", "clusters/prod/addons"}

	tests := []struct {
		name    string
		entries []models.GitDirectory
		want    []string
	}{
		{
			// `**` is not recursive: path.Match gives it the same meaning as `*`.
			name:    "double star is not recursive",
			entries: []models.GitDirectory{include("clusters/**")},
			want:    []string{"clusters/dev", "clusters/prod"},
		},
		{
			name:    "trailing slash matches nothing",
			entries: []models.GitDirectory{include("clusters/*/")},
			want:    nil,
		},
		{
			name:    "malformed include matches nothing",
			entries: []models.GitDirectory{include("clusters/[")},
			want:    nil,
		},
		{
			// A malformed exclude cannot exclude, so the includes stand.
			name:    "malformed exclude excludes nothing",
			entries: []models.GitDirectory{include("clusters/*"), exclude("clusters/[")},
			want:    []string{"clusters/dev", "clusters/prod"},
		},
		{
			name:    "overlapping includes yield each directory once",
			entries: []models.GitDirectory{include("apps/*"), include("apps/guestbook")},
			want:    []string{"apps/guestbook", "apps/kustomize"},
		},
		{
			name:    "no entries",
			entries: nil,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, matchDirectories(dirs, tt.entries))
		})
	}
}

// TestExpandRejectsDuplicateNamesFromGitDirectories covers the collision the
// idiomatic `{{ .path.basename }}` name template invites.
func TestExpandRejectsDuplicateNamesFromGitDirectories(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://example.com/repo.git
        directories:
          - path: apps/*
          - path: clusters/*
  template:
    metadata:
      name: '{{ .path.basename }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	_, err := Expand(appSet, dirTree("apps/dev", "clusters/dev"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

// TestExpandRejectsNormalizationCollisions proves basenameNormalized collapses
// distinct directories into one name, which must still be caught.
func TestExpandRejectsNormalizationCollisions(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://example.com/repo.git
        directories:
          - path: clusters/*
  template:
    metadata:
      name: '{{ .path.basenameNormalized }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	_, err := Expand(appSet, dirTree("clusters/eu-west-1", "clusters/eu_west_1"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}
