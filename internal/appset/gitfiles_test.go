package appset

import (
	"fmt"
	"testing"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fileEntry(path string) models.GitFile {
	return models.GitFile{Path: path}
}

// TestMatchFiles covers the difference that matters most against the directory
// generator: file patterns are matched with doublestar, so ** recurses.
func TestMatchFiles(t *testing.T) {
	files := []string{
		"clusters/dev/config.yaml",
		"clusters/dev/nested/config.yaml",
		"clusters/prod/config.yaml",
		"clusters/prod/values.yaml",
		"apps/guestbook/config.yaml",
		".github/workflows/config.yaml",
	}

	tests := []struct {
		name    string
		entries []models.GitFile
		want    []string
	}{
		{
			name:    "double star recurses, unlike a directory pattern",
			entries: []models.GitFile{fileEntry("clusters/**/config.yaml")},
			want:    []string{"clusters/dev/config.yaml", "clusters/dev/nested/config.yaml", "clusters/prod/config.yaml"},
		},
		{
			name:    "single star stays at one level",
			entries: []models.GitFile{fileEntry("clusters/*/config.yaml")},
			want:    []string{"clusters/dev/config.yaml", "clusters/prod/config.yaml"},
		},
		{
			name:    "exact path",
			entries: []models.GitFile{fileEntry("clusters/prod/values.yaml")},
			want:    []string{"clusters/prod/values.yaml"},
		},
		{
			name:    "several entries are unioned and sorted",
			entries: []models.GitFile{fileEntry("apps/**/config.yaml"), fileEntry("clusters/*/values.yaml")},
			want:    []string{"apps/guestbook/config.yaml", "clusters/prod/values.yaml"},
		},
		{
			// ArgoCD filters dot directories for the directories generator only;
			// LsFiles globs files without that filter, so this follows it.
			name:    "a file under a dot directory matches if the pattern says so",
			entries: []models.GitFile{fileEntry("**/config.yaml")},
			want: []string{".github/workflows/config.yaml", "apps/guestbook/config.yaml",
				"clusters/dev/config.yaml", "clusters/dev/nested/config.yaml", "clusters/prod/config.yaml"},
		},
		{
			name:    "a dot directory can be named outright",
			entries: []models.GitFile{fileEntry(".github/**/config.yaml")},
			want:    []string{".github/workflows/config.yaml"},
		},
		{
			name:    "overlapping entries yield each file once",
			entries: []models.GitFile{fileEntry("clusters/**/config.yaml"), fileEntry("clusters/dev/config.yaml")},
			want:    []string{"clusters/dev/config.yaml", "clusters/dev/nested/config.yaml", "clusters/prod/config.yaml"},
		},
		{
			name:    "no entries",
			entries: nil,
			want:    nil,
		},
		{
			name:    "no match",
			entries: []models.GitFile{fileEntry("missing/**/*.yaml")},
			want:    nil,
		},
		{
			name:    "malformed pattern matches nothing",
			entries: []models.GitFile{fileEntry("clusters/[")},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, matchFiles(files, tt.entries))
		})
	}
}

// TestFileParamsFromMapping pins the shape ArgoCD builds: the file's contents
// at the top level, and a path block describing the containing directory.
func TestFileParamsFromMapping(t *testing.T) {
	params, err := fileParams("clusters/eu-west-1/Prod_Cluster/config.yaml",
		[]byte("cluster:\n  name: prod\n  replicas: 3\n"))

	require.NoError(t, err)
	require.Len(t, params, 1)

	cluster, ok := params[0]["cluster"].(map[string]any)
	require.True(t, ok, "file contents land at the top level")
	assert.Equal(t, "prod", cluster["name"])
	// Parsed with sigs.k8s.io/yaml, as ArgoCD does, so numbers arrive as float64.
	assert.Equal(t, float64(3), cluster["replicas"])

	path, ok := params[0]["path"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "clusters/eu-west-1/Prod_Cluster", path["path"], "path describes the directory")
	assert.Equal(t, []string{"clusters", "eu-west-1", "Prod_Cluster"}, path["segments"])
	assert.Equal(t, "Prod_Cluster", path["basename"])
	assert.Equal(t, "prod-cluster", path["basenameNormalized"])
	assert.Equal(t, "config.yaml", path["filename"])
	assert.Equal(t, "config.yaml", path["filenameNormalized"])
}

// TestFileParamsFromSequence covers a file holding a list, which ArgoCD turns
// into one parameter set per element and so one Application per element.
func TestFileParamsFromSequence(t *testing.T) {
	params, err := fileParams("clusters/config.yaml",
		[]byte("- name: dev\n- name: prod\n"))

	require.NoError(t, err)
	require.Len(t, params, 2)
	assert.Equal(t, "dev", params[0]["name"])
	assert.Equal(t, "prod", params[1]["name"])
	path, ok := params[0]["path"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "clusters", path["path"])
}

// TestFileParamsAcceptsJSON proves a JSON file works, since the parser reads
// JSON as a subset of YAML.
func TestFileParamsAcceptsJSON(t *testing.T) {
	params, err := fileParams("clusters/dev/config.json", []byte(`{"cluster":{"name":"dev"}}`))

	require.NoError(t, err)
	require.Len(t, params, 1)
	assert.Equal(t, map[string]any{"name": "dev"}, params[0]["cluster"])
	path, ok := params[0]["path"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "config.json", path["filename"])
}

func TestFileParamsRejectsUnparseableContent(t *testing.T) {
	_, err := fileParams("clusters/dev/config.yaml", []byte("a: [unclosed\n"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "clusters/dev/config.yaml")
}

// TestFileParamsAtRepositoryRoot pins the awkward shape ArgoCD produces for a
// file with no directory above it: path.Dir gives ".", which normalizes away to
// nothing. A template naming basenameNormalized there gets an empty string.
func TestFileParamsAtRepositoryRoot(t *testing.T) {
	params, err := fileParams("config.yaml", []byte("name: root\n"))

	require.NoError(t, err)
	require.Len(t, params, 1)

	path, ok := params[0]["path"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, ".", path["path"])
	assert.Equal(t, []string{"."}, path["segments"])
	assert.Equal(t, ".", path["basename"])
	assert.Empty(t, path["basenameNormalized"])
	assert.Equal(t, "config.yaml", path["filename"])
	assert.Equal(t, "config.yaml", path["filenameNormalized"])
}

// TestFileParamsContentShapes pins what each well-formed but non-mapping file
// produces. ArgoCD decodes a single object first, noting that this "will also
// succeed for empty files", so an empty file yields one parameter set.
func TestFileParamsContentShapes(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
		wantErr bool
	}{
		{name: "empty file", content: "", want: 1},
		{name: "comment only", content: "# nothing here\n", want: 1},
		{name: "explicit null", content: "null\n", want: 1},
		{name: "empty sequence", content: "[]\n", want: 0},
		{name: "sequence of mappings", content: "- a: 1\n- a: 2\n", want: 2},
		{name: "sequence of scalars", content: "- a\n- b\n", wantErr: true},
		{name: "bare scalar", content: "just a string\n", wantErr: true},
		{name: "number", content: "42\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, err := fileParams("clusters/dev/config.yaml", []byte(tt.content))

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "clusters/dev/config.yaml")
				return
			}

			require.NoError(t, err)
			assert.Len(t, params, tt.want)
		})
	}
}

// fakeTree serves fixed paths and contents to the expansion, and counts the
// listings so a test can prove the tree is read once rather than per generator.
type fakeTree struct {
	dirs      []string
	files     []string
	contents  map[string]string
	dirErr    error
	fileErr   error
	readErr   error
	dirCalls  int
	fileCalls int
}

func (f *fakeTree) Directories() ([]string, error) {
	f.dirCalls++
	return f.dirs, f.dirErr
}

func (f *fakeTree) Files() ([]string, error) {
	f.fileCalls++
	return f.files, f.fileErr
}

func (f *fakeTree) ReadFile(path string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	content, ok := f.contents[path]
	if !ok {
		return nil, fmt.Errorf("no such file %q", path)
	}
	return []byte(content), nil
}

// dirTree is the common case: a tree whose directories are all a test needs.
func dirTree(dirs ...string) *fakeTree {
	return &fakeTree{dirs: dirs}
}

// TestExpandGitFileGenerator covers the whole path: match, read, parse, and
// expose the file's contents as parameters.
func TestExpandGitFileGenerator(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - git:
        repoURL: https://example.com/repo.git
        files:
          - path: clusters/**/config.yaml
  template:
    metadata:
      name: '{{ .cluster.name }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: '{{ .cluster.version }}'
        helm:
          valueFiles:
            - '{{ .path.path }}/values.yaml'
`)

	apps, err := Expand(appSet, &fakeTree{
		files: []string{"clusters/dev/config.yaml", "clusters/prod/config.yaml", "apps/other/config.yaml"},
		contents: map[string]string{
			"clusters/dev/config.yaml":  "cluster:\n  name: dev\n  version: 1.0.0\n",
			"clusters/prod/config.yaml": "cluster:\n  name: prod\n  version: 2.0.0\n",
		},
	})

	require.NoError(t, err)
	require.Len(t, apps, 2)
	assert.Equal(t, "dev", apps[0].Metadata.Name)
	assert.Equal(t, "1.0.0", apps[0].Spec.Source.TargetRevision)
	assert.Equal(t, []string{"clusters/dev/values.yaml"}, apps[0].Spec.Source.Helm.ValueFiles)
	assert.Equal(t, "prod", apps[1].Metadata.Name)
}

// TestExpandGitFileGeneratorSplitsSequences proves one file holding a list
// generates one Application per element.
func TestExpandGitFileGeneratorSplitsSequences(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://example.com/repo.git
        files:
          - path: clusters.yaml
  template:
    metadata:
      name: '{{ .name }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	apps, err := Expand(appSet, &fakeTree{
		files:    []string{"clusters.yaml"},
		contents: map[string]string{"clusters.yaml": "- name: dev\n- name: prod\n"},
	})

	require.NoError(t, err)
	require.Len(t, apps, 2)
	assert.Equal(t, "dev", apps[0].Metadata.Name)
	assert.Equal(t, "prod", apps[1].Metadata.Name)
}

// TestExpandAppliesGeneratorValues covers the values block, which is rendered
// against the parameters built so far and exposed as .values.
func TestExpandAppliesGeneratorValues(t *testing.T) {
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
        values:
          label: '{{ .path.basename }}-addons'
          chart: guestbook
  template:
    metadata:
      name: '{{ .values.label }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: '{{ .values.chart }}'
        targetRevision: 1.0.0
`)

	apps, err := Expand(appSet, dirTree("clusters/dev"))

	require.NoError(t, err)
	require.Len(t, apps, 1)
	assert.Equal(t, "dev-addons", apps[0].Metadata.Name)
	assert.Equal(t, "guestbook", apps[0].Spec.Source.Chart)
}

// TestExpandValuesCannotReferenceEachOther pins ArgoCD's guard: entries render
// against the parameters built so far, never against a sibling entry.
func TestExpandValuesCannotReferenceEachOther(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - git:
        repoURL: https://example.com/repo.git
        directories:
          - path: clusters/*
        values:
          first: '{{ .path.basename }}'
          second: '{{ .values.first }}'
  template:
    metadata:
      name: '{{ .values.second }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	_, err := Expand(appSet, dirTree("clusters/dev"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "values.second")
}

func TestExpandReportsFileReadFailures(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://example.com/repo.git
        files:
          - path: clusters/*.yaml
  template:
    metadata:
      name: '{{ .name }}'
`)

	_, err := Expand(appSet, &fakeTree{
		files:   []string{"clusters/dev.yaml"},
		readErr: assert.AnError,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read clusters/dev.yaml for git generator")
}

func TestExpandReportsFileListingFailures(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://example.com/repo.git
        files:
          - path: clusters/*.yaml
  template:
    metadata:
      name: '{{ .name }}'
`)

	_, err := Expand(appSet, &fakeTree{fileErr: assert.AnError})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "list files for git generator")
}

// TestMatchesFile covers the gate discovery uses to decide whether a changed
// file reaches a file generator at all.
func TestMatchesFile(t *testing.T) {
	gitGen := &models.GitGenerator{Files: []models.GitFile{fileEntry("clusters/**/config.yaml")}}

	tests := []struct {
		name   string
		gitGen *models.GitGenerator
		file   string
		want   bool
	}{
		{name: "nil generator", gitGen: nil, file: "clusters/dev/config.yaml"},
		{name: "no entries", gitGen: &models.GitGenerator{}, file: "clusters/dev/config.yaml"},
		{name: "matching file", gitGen: gitGen, file: "clusters/dev/config.yaml", want: true},
		{name: "double star recurses", gitGen: gitGen, file: "clusters/a/b/config.yaml", want: true},
		{name: "different name", gitGen: gitGen, file: "clusters/dev/values.yaml"},
		{name: "outside the pattern", gitGen: gitGen, file: "apps/dev/config.yaml"},
		// Unlike a directories pattern, a dot segment is not filtered for files.
		{name: "dot segment matches when the pattern covers it", gitGen: gitGen, file: "clusters/.hidden/config.yaml", want: true},
		{name: "empty path", gitGen: gitGen, file: ""},
		{
			name:   "a directory generator matches no file",
			gitGen: &models.GitGenerator{Directories: []models.GitDirectory{include("clusters/*")}},
			file:   "clusters/dev/config.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, MatchesFile(tt.gitGen, tt.file))
		})
	}
}

// TestExpandReadsTheTreeOnceForSeveralFileGenerators keeps a manifest with two
// file generators from listing the same tree twice.
func TestExpandReadsTheTreeOnceForSeveralFileGenerators(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://example.com/repo.git
        files:
          - path: apps/*.yaml
    - git:
        repoURL: https://example.com/repo.git
        files:
          - path: clusters/*.yaml
  template:
    metadata:
      name: '{{ .name }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	tree := &fakeTree{
		files: []string{"apps/one.yaml", "clusters/two.yaml"},
		contents: map[string]string{
			"apps/one.yaml":     "name: one\n",
			"clusters/two.yaml": "name: two\n",
		},
	}

	apps, err := Expand(appSet, tree)
	require.NoError(t, err)
	require.Len(t, apps, 2)
	assert.Equal(t, 1, tree.fileCalls)
	assert.Zero(t, tree.dirCalls, "a file generator never lists directories")
}

// TestExpandMixesListDirectoryAndFileGenerators guards the collector's two
// independent listing caches and the order it concatenates shapes in.
func TestExpandMixesListDirectoryAndFileGenerators(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: mixed
spec:
  goTemplate: true
  generators:
    - list:
        elements:
          - name: from-list
    - git:
        repoURL: https://example.com/repo.git
        directories:
          - path: clusters/*
    - git:
        repoURL: https://example.com/repo.git
        files:
          - path: apps/*.yaml
  template:
    metadata:
      name: '{{ dig "name" .path.basename . }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	tree := &fakeTree{
		dirs:     []string{"clusters/prod", "clusters/dev"},
		files:    []string{"apps/two.yaml", "apps/one.yaml"},
		contents: map[string]string{"apps/one.yaml": "name: one\n", "apps/two.yaml": "name: two\n"},
	}

	apps, err := Expand(appSet, tree)
	require.NoError(t, err)

	names := make([]string, 0, len(apps))
	for _, app := range apps {
		names = append(names, app.Metadata.Name)
	}

	// Declaration order, with each generator's own matches sorted.
	assert.Equal(t, []string{"from-list", "dev", "prod", "one", "two"}, names)
	assert.Equal(t, 1, tree.dirCalls)
	assert.Equal(t, 1, tree.fileCalls)
}

// TestExpandAppliesValuesToFileParameters proves a values entry can read the
// matched file's own contents, not just its path.
func TestExpandAppliesValuesToFileParameters(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - git:
        repoURL: https://example.com/repo.git
        files:
          - path: clusters/*/config.yaml
        values:
          label: '{{ .cluster.name }}-addons'
  template:
    metadata:
      name: '{{ .values.label }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	apps, err := Expand(appSet, &fakeTree{
		files:    []string{"clusters/dev/config.yaml"},
		contents: map[string]string{"clusters/dev/config.yaml": "cluster:\n  name: dev\n"},
	})

	require.NoError(t, err)
	require.Len(t, apps, 1)
	assert.Equal(t, "dev-addons", apps[0].Metadata.Name)
}

// TestExpandGeneratedBlocksOverrideFileKeys pins which side wins when a file's
// own contents carry keys the generator also produces.
func TestExpandGeneratedBlocksOverrideFileKeys(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://example.com/repo.git
        files:
          - path: clusters/*/config.yaml
        values:
          label: generated
  template:
    metadata:
      name: '{{ .values.label }}-{{ .path.basename }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	apps, err := Expand(appSet, &fakeTree{
		files: []string{"clusters/dev/config.yaml"},
		contents: map[string]string{
			"clusters/dev/config.yaml": "path: from-file\nvalues:\n  label: from-file\n",
		},
	})

	require.NoError(t, err)
	require.Len(t, apps, 1)
	assert.Equal(t, "generated-dev", apps[0].Metadata.Name)
}
