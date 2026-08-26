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
