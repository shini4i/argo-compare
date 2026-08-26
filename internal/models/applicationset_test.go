package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// listAppSet is a minimal, valid list-generator ApplicationSet used as the
// starting point for the negative cases below.
const listAppSet = `
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: guestbook
  namespace: argocd
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - list:
        elements:
          - cluster: engineering-dev
            url: https://kubernetes.default.svc
          - cluster: engineering-prod
            url: https://kubernetes.default.svc
  template:
    metadata:
      name: '{{.cluster}}-guestbook'
    spec:
      destination:
        server: '{{.url}}'
        namespace: guestbook
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`

func parseAppSet(t *testing.T, manifest string) *ApplicationSet {
	t.Helper()

	var appSet ApplicationSet
	require.NoError(t, yaml.Unmarshal([]byte(manifest), &appSet))
	return &appSet
}

func TestApplicationSetValidateAcceptsListGenerator(t *testing.T) {
	appSet := parseAppSet(t, listAppSet)

	require.NoError(t, appSet.Validate())

	require.Len(t, appSet.Spec.Generators, 1)
	require.NotNil(t, appSet.Spec.Generators[0].List)

	elements := appSet.Spec.Generators[0].List.Elements
	require.Len(t, elements, 2)
	assert.Equal(t, "engineering-dev", elements[0]["cluster"])
	assert.Equal(t, []string{"missingkey=error"}, appSet.Spec.GoTemplateOptions)
}

func TestApplicationSetValidateRejectsOtherKinds(t *testing.T) {
	appSet := parseAppSet(t, `
kind: Application
metadata:
  name: guestbook
`)

	assert.ErrorIs(t, appSet.Validate(), ErrNotApplicationSet)
}

func TestApplicationSetValidateRejectsEmptyManifest(t *testing.T) {
	var appSet *ApplicationSet
	assert.ErrorIs(t, appSet.Validate(), ErrEmptyFile)

	assert.ErrorIs(t, (&ApplicationSet{}).Validate(), ErrEmptyFile)
}

// TestApplicationSetValidateRejectsLegacyTemplating guards the deliberate scope
// boundary: fasttemplate manifests are skipped rather than rendered with the
// wrong engine.
func TestApplicationSetValidateRejectsLegacyTemplating(t *testing.T) {
	appSet := parseAppSet(t, `
kind: ApplicationSet
metadata:
  name: guestbook
spec:
  generators:
    - list:
        elements:
          - cluster: dev
  template:
    metadata:
      name: '{{cluster}}'
`)

	err := appSet.Validate()
	assert.ErrorIs(t, err, ErrUnsupportedAppConfiguration)
	assert.Contains(t, err.Error(), "goTemplate: true")
}

func TestApplicationSetValidateRejectsUnsupportedGenerators(t *testing.T) {
	tests := []struct {
		name       string
		generators string
		wantErrMsg string
	}{
		{
			name: "unsupported kind",
			generators: `
    - matrix:
        generators: []`,
			wantErrMsg: "matrix",
		},
		{
			name: "multiple kinds in one entry",
			generators: `
    - list:
        elements:
          - cluster: dev
      clusters: {}`,
			wantErrMsg: "exactly one generator",
		},
		{
			name:       "no generators",
			generators: ` []`,
			wantErrMsg: "at least one generator",
		},
		{
			name: "elementsYaml",
			generators: `
    - list:
        elementsYaml: '{{ .someKey | toJson }}'`,
			wantErrMsg: "elementsYaml",
		},
		{
			name: "generator level template override",
			generators: `
    - list:
        elements:
          - cluster: dev
        template:
          metadata:
            name: override`,
			wantErrMsg: "generator-level template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appSet := parseAppSet(t, `
kind: ApplicationSet
metadata:
  name: guestbook
spec:
  goTemplate: true
  generators:`+tt.generators+`
  template:
    metadata:
      name: '{{.cluster}}'
`)

			err := appSet.Validate()
			assert.ErrorIs(t, err, ErrUnsupportedAppConfiguration)
			assert.Contains(t, err.Error(), tt.wantErrMsg)
		})
	}
}

func TestApplicationSetValidateRequiresTemplate(t *testing.T) {
	appSet := parseAppSet(t, `
kind: ApplicationSet
metadata:
  name: guestbook
spec:
  goTemplate: true
  generators:
    - list:
        elements:
          - cluster: dev
`)

	err := appSet.Validate()
	assert.ErrorIs(t, err, ErrUnsupportedAppConfiguration)
	assert.Contains(t, err.Error(), "spec.template")
}

// TestApplicationSetTemplateRetainsRawNode verifies the template subtree
// survives parsing as YAML that can be re-marshalled for rendering.
func TestApplicationSetTemplateRetainsRawNode(t *testing.T) {
	appSet := parseAppSet(t, listAppSet)
	require.NoError(t, appSet.Validate())

	rendered, err := yaml.Marshal(&appSet.Spec.Template)
	require.NoError(t, err)
	assert.Contains(t, string(rendered), "{{.cluster}}-guestbook")
	assert.Contains(t, string(rendered), "chart: guestbook")
}

// TestApplicationSetMalformedGeneratorIsAnError separates the two outcomes:
// a structurally broken generators block is a decode error (the file is
// reported invalid), not the warn-and-skip an unsupported generator gets.
func TestApplicationSetMalformedGeneratorIsAnError(t *testing.T) {
	tests := []struct {
		name       string
		manifest   string
		wantErrMsg string
	}{
		{
			name: "generator entry is not a mapping",
			manifest: `
kind: ApplicationSet
spec:
  generators:
    - list
`,
			wantErrMsg: "generator entry must be a mapping",
		},
		{
			name: "list generator is not a mapping",
			manifest: `
kind: ApplicationSet
spec:
  generators:
    - list: "not-a-mapping"
`,
			wantErrMsg: "decode list generator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var appSet ApplicationSet
			err := yaml.Unmarshal([]byte(tt.manifest), &appSet)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErrMsg)
			assert.NotErrorIs(t, err, ErrUnsupportedAppConfiguration)
		})
	}
}

func TestApplicationSetValidateAcceptsGitDirectoryGenerator(t *testing.T) {
	appSet := parseAppSet(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
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
      name: '{{ .path.basename }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	require.NoError(t, appSet.Validate())

	gitGen := appSet.Spec.Generators[0].Git
	require.NotNil(t, gitGen)
	require.Len(t, gitGen.Directories, 2)
	assert.Equal(t, "clusters/*", gitGen.Directories[0].Path)
	assert.False(t, gitGen.Directories[0].Exclude)
	assert.True(t, gitGen.Directories[1].Exclude)
}

func TestApplicationSetValidateRejectsUnsupportedGitFields(t *testing.T) {
	tests := []struct {
		name       string
		generator  string
		wantErrMsg string
	}{
		{
			name: "pathParamPrefix",
			generator: `
        repoURL: https://example.com/repo.git
        directories:
          - path: 'clusters/*'
        pathParamPrefix: mycluster`,
			wantErrMsg: "'pathParamPrefix' is not supported",
		},
		{
			name: "missing repoURL",
			generator: `
        directories:
          - path: 'clusters/*'`,
			wantErrMsg: "requires repoURL",
		},
		{
			name: "neither directories nor files",
			generator: `
        repoURL: https://example.com/repo.git`,
			wantErrMsg: "requires a 'directories' or 'files' entry",
		},
		{
			name: "both directories and files",
			generator: `
        repoURL: https://example.com/repo.git
        directories:
          - path: 'clusters/*'
        files:
          - path: 'clusters/**/config.yaml'`,
			wantErrMsg: "each generator reads one or the other",
		},
		{
			name: "file without a path",
			generator: `
        repoURL: https://example.com/repo.git
        files:
          - {}`,
			wantErrMsg: "every git generator 'files' entry requires a path",
		},
		{
			name: "directory without a path",
			generator: `
        repoURL: https://example.com/repo.git
        directories:
          - exclude: true`,
			wantErrMsg: "requires a path",
		},
		{
			name: "generator level template override",
			generator: `
        repoURL: https://example.com/repo.git
        directories:
          - path: 'clusters/*'
        template:
          metadata:
            name: override`,
			wantErrMsg: "generator-level template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appSet := parseAppSet(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:`+tt.generator+`
  template:
    metadata:
      name: '{{ .path.basename }}'
`)

			err := appSet.Validate()
			assert.ErrorIs(t, err, ErrUnsupportedAppConfiguration)
			assert.Contains(t, err.Error(), tt.wantErrMsg)
		})
	}
}

// TestApplicationSetGitGeneratorDecodeError pins that a malformed git generator
// is a decode error, not the warn-and-skip an unsupported one gets.
func TestApplicationSetGitGeneratorDecodeError(t *testing.T) {
	var appSet ApplicationSet
	err := yaml.Unmarshal([]byte(`
kind: ApplicationSet
spec:
  generators:
    - git: "not-a-mapping"
`), &appSet)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode git generator")
	assert.NotErrorIs(t, err, ErrUnsupportedAppConfiguration)
}

// TestApplicationSetKeepsGitRevision proves revision survives decoding. Whether
// a given revision can be compared is decided in the app layer, which knows the
// target branch; see assertGitGeneratorsComparable.
func TestApplicationSetKeepsGitRevision(t *testing.T) {
	appSet := parseAppSet(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://example.com/repo.git
        revision: v1.2.3
        directories:
          - path: 'clusters/*'
  template:
    metadata:
      name: '{{ .path.basename }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: demo
        targetRevision: 1.0.0
`)

	require.NoError(t, appSet.Validate())
	assert.Equal(t, "v1.2.3", appSet.Spec.Generators[0].Git.Revision)
}

// TestApplicationSetRejectsMalformedGitPatterns keeps a broken pattern from
// matching nothing in silence, which would compare fewer Applications than the
// manifest asks for and report nothing about it.
func TestApplicationSetRejectsMalformedGitPatterns(t *testing.T) {
	tests := []struct {
		name       string
		generator  string
		wantErrMsg string
	}{
		{
			name: "malformed directories pattern",
			generator: `
        repoURL: https://example.com/repo.git
        directories:
          - path: 'clusters/['`,
			wantErrMsg: `'directories' pattern "clusters/[" is malformed`,
		},
		{
			name: "malformed exclude pattern",
			generator: `
        repoURL: https://example.com/repo.git
        directories:
          - path: 'clusters/*'
          - path: 'clusters/a[b-'
            exclude: true`,
			wantErrMsg: `'directories' pattern "clusters/a[b-" is malformed`,
		},
		{
			name: "malformed files pattern",
			generator: `
        repoURL: https://example.com/repo.git
        files:
          - path: 'clusters/['`,
			wantErrMsg: `'files' pattern "clusters/[" is malformed`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appSet := parseAppSet(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:`+tt.generator+`
  template:
    metadata:
      name: '{{ .path.basename }}'
`)

			err := appSet.Validate()
			assert.ErrorIs(t, err, ErrUnsupportedAppConfiguration)
			assert.Contains(t, err.Error(), tt.wantErrMsg)
		})
	}
}

// TestApplicationSetAcceptsRecursiveFilePatterns keeps the validator from
// rejecting `**`, which is exactly what a files pattern is written with.
func TestApplicationSetAcceptsRecursiveFilePatterns(t *testing.T) {
	appSet := parseAppSet(t, `
kind: ApplicationSet
metadata:
  name: cluster-addons
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://example.com/repo.git
        files:
          - path: 'clusters/**/config.yaml'
          - path: '**'
  template:
    metadata:
      name: '{{ .name }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: demo
        targetRevision: 1.0.0
`)

	assert.NoError(t, appSet.Validate())
}
