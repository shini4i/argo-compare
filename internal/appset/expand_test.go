package appset

import (
	"testing"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func mustParse(t *testing.T, manifest string) *models.ApplicationSet {
	t.Helper()

	var appSet models.ApplicationSet
	require.NoError(t, yaml.Unmarshal([]byte(manifest), &appSet))
	return &appSet
}

func TestExpandListGenerator(t *testing.T) {
	appSet := mustParse(t, `
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
            url: https://dev.example.com
            revision: 1.0.0
          - cluster: engineering-prod
            url: https://prod.example.com
            revision: 2.0.0
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
        targetRevision: '{{.revision}}'
`)

	apps, err := Expand(appSet, nil)
	require.NoError(t, err)
	require.Len(t, apps, 2)

	assert.Equal(t, models.KindApplication, apps[0].Kind)
	assert.Equal(t, "engineering-dev-guestbook", apps[0].Metadata.Name)
	assert.Equal(t, "https://dev.example.com", apps[0].Spec.Destination.Server)
	assert.Equal(t, "1.0.0", apps[0].Spec.Source.TargetRevision)

	assert.Equal(t, "engineering-prod-guestbook", apps[1].Metadata.Name)
	assert.Equal(t, "2.0.0", apps[1].Spec.Source.TargetRevision)
}

func TestExpandConcatenatesGenerators(t *testing.T) {
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
    - list:
        elements:
          - cluster: prod
  template:
    metadata:
      name: '{{.cluster}}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	apps, err := Expand(appSet, nil)
	require.NoError(t, err)
	require.Len(t, apps, 2)
	assert.Equal(t, "dev", apps[0].Metadata.Name)
	assert.Equal(t, "prod", apps[1].Metadata.Name)
}

func TestExpandRejectsDuplicateNames(t *testing.T) {
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
          - cluster: dev
  template:
    metadata:
      name: '{{.cluster}}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	_, err := Expand(appSet, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestExpandPropagatesValidationErrors(t *testing.T) {
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
      name: '{{.cluster}}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        path: charts/guestbook
        targetRevision: 1.0.0
`)

	_, err := Expand(appSet, nil)
	assert.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
}

func TestExpandRejectsUnsupportedApplicationSet(t *testing.T) {
	appSet := mustParse(t, `
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

	_, err := Expand(appSet, nil)
	assert.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
}

// TestExpandMissingKeyIsFatalWithOption pins the documented recommendation:
// goTemplateOptions: ["missingkey=error"] turns an undefined parameter into a
// failure instead of an empty string.
func TestExpandMissingKeyIsFatalWithOption(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: guestbook
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - list:
        elements:
          - cluster: dev
  template:
    metadata:
      name: '{{.cluster}}-{{.missing}}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	_, err := Expand(appSet, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestExpandSupportsSprigAndNormalize(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: guestbook
spec:
  goTemplate: true
  generators:
    - list:
        elements:
          - cluster: Feature/ABC_123
  template:
    metadata:
      name: '{{ .cluster | normalize }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: '{{ dig "chart" "guestbook" . }}'
        targetRevision: '{{ default "1.0.0" .revision }}'
`)

	apps, err := Expand(appSet, nil)
	require.NoError(t, err)
	require.Len(t, apps, 1)
	assert.Equal(t, "feature-abc-123", apps[0].Metadata.Name)
	assert.Equal(t, "guestbook", apps[0].Spec.Source.Chart)
	assert.Equal(t, "1.0.0", apps[0].Spec.Source.TargetRevision)
}

// TestExpandDeniesEnvironmentFunctions guards the boundary that keeps a
// manifest author from reading CI secrets (REPO_CREDS_*) into a rendered diff
// that argo-compare may post as a merge request comment.
func TestExpandDeniesEnvironmentFunctions(t *testing.T) {
	for _, fn := range []string{"env", "expandenv", "getHostByName"} {
		t.Run(fn, func(t *testing.T) {
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
      name: '{{ `+fn+` "HOME" }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

			_, err := Expand(appSet, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not defined")
		})
	}
}

func TestExpandKeepsSpecialCharactersInValues(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: guestbook
spec:
  goTemplate: true
  generators:
    - list:
        elements:
          - cluster: "it's broken"
  template:
    metadata:
      name: '{{.cluster}}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	apps, err := Expand(appSet, nil)
	require.NoError(t, err)
	require.Len(t, apps, 1)
	assert.Equal(t, "it's broken", apps[0].Metadata.Name)
}

// TestExpandRejectsUnknownTemplateOption keeps an unrecognised goTemplateOptions
// entry from reaching text/template.Option, which panics on one.
func TestExpandRejectsUnknownTemplateOption(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: guestbook
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=bogus"]
  generators:
    - list:
        elements:
          - cluster: dev
  template:
    metadata:
      name: '{{.cluster}}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	_, err := Expand(appSet, nil)
	assert.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
	assert.Contains(t, err.Error(), "missingkey=bogus")
}

// TestExpandWithNoElementsYieldsNothing pins the empty-generator case: emptying
// the element list is how a user removes every generated Application, and the
// pairing step must see an empty source side rather than an error.
func TestExpandWithNoElementsYieldsNothing(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: guestbook
spec:
  goTemplate: true
  generators:
    - list:
        elements: []
  template:
    metadata:
      name: '{{.cluster}}'
`)

	apps, err := Expand(appSet, nil)
	require.NoError(t, err)
	assert.Empty(t, apps)
}

// TestExpandErrorNamesTheElement keeps a bad element identifiable when the
// rendered Application has no name to report.
func TestExpandErrorNamesTheElement(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: guestbook
spec:
  goTemplate: true
  generators:
    - list:
        elements:
          - cluster: ok
          - cluster: broken
  template:
    metadata:
      name: '{{ .cluster }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: '{{ if eq .cluster "ok" }}guestbook{{ end }}'
        targetRevision: 1.0.0
`)

	_, err := Expand(appSet, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generator element 1")
}

// TestExpandSupportsArgoCDTemplateFunctions proves the functions ArgoCD adds on
// top of sprig are reachable from a real manifest, not just callable in Go.
func TestExpandSupportsArgoCDTemplateFunctions(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: guestbook
spec:
  goTemplate: true
  generators:
    - list:
        elements:
          - branch: Feature/ABC 123
            config: '{"chart":"guestbook","version":"1.2.3"}'
            extras:
              - one
              - two
  template:
    metadata:
      name: '{{ cat .branch | slugify 11 }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: '{{ (fromYaml .config).chart }}'
        targetRevision: '{{ (fromYaml .config).version }}'
        helm:
          releaseName: '{{ index (fromYamlArray (toYaml .extras)) 1 }}'
`)

	apps, err := Expand(appSet, nil)
	require.NoError(t, err)
	require.Len(t, apps, 1)

	assert.Equal(t, "feature-abc", apps[0].Metadata.Name)
	assert.Equal(t, "guestbook", apps[0].Spec.Source.Chart)
	assert.Equal(t, "1.2.3", apps[0].Spec.Source.TargetRevision)
	// toYaml re-serialized the list and fromYamlArray parsed it back.
	assert.Equal(t, "two", apps[0].Spec.Source.Helm.ReleaseName)
}

// TestExpandRendersToYamlIntoAStringField covers toYaml's main use. Because
// each field is rendered on its own, the value is just a string and needs no
// indentation ceremony to survive.
func TestExpandRendersToYamlIntoAStringField(t *testing.T) {
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
            values:
              replicaCount: 2
              image:
                tag: 1.0.0
  template:
    metadata:
      name: '{{ .cluster }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
        helm:
          values: '{{ toYaml .values }}'
`)

	apps, err := Expand(appSet, nil)
	require.NoError(t, err)
	require.Len(t, apps, 1)

	assert.Equal(t, "image:\n  tag: 1.0.0\nreplicaCount: 2", apps[0].Spec.Source.Helm.Values)
}

// TestExpandNindentUsesTheAuthorsColumn proves nindent means what the author
// wrote. It once had to match argo-compare's re-marshalled layout instead, and
// a wrong guess emptied the field with no error.
func TestExpandNindentUsesTheAuthorsColumn(t *testing.T) {
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
            values:
              replicaCount: 2
  template:
    metadata:
      name: '{{ .cluster }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
        helm:
          values: '{{ toYaml .values | nindent 4 }}'
`)

	apps, err := Expand(appSet, nil)
	require.NoError(t, err)
	require.Len(t, apps, 1)

	assert.Equal(t, "\n    replicaCount: 2", apps[0].Spec.Source.Helm.Values)
}

// TestExpandRendersValuesObject covers helm.valuesObject, which is free-form
// YAML and so needs rendering at any depth rather than only at the top level.
func TestExpandRendersValuesObject(t *testing.T) {
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
            tag: 2.1.0
  template:
    metadata:
      name: '{{ .cluster }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
        helm:
          valueFiles:
            - 'values-{{ .cluster }}.yaml'
          parameters:
            - name: 'image.tag'
              value: '{{ .tag }}'
          valuesObject:
            image:
              tag: '{{ .tag }}'
              hosts:
                - '{{ .cluster }}.example.com'
`)

	apps, err := Expand(appSet, nil)
	require.NoError(t, err)
	require.Len(t, apps, 1)

	helm := apps[0].Spec.Source.Helm
	assert.Equal(t, []string{"values-dev.yaml"}, helm.ValueFiles)
	assert.Equal(t, "2.1.0", helm.Parameters[0].Value)

	image, ok := helm.ValuesObject["image"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "2.1.0", image["tag"])
	assert.Equal(t, []any{"dev.example.com"}, image["hosts"])
}

// TestExpandRejectsTplFunction pins the documented gap: ArgoCD's tpl is not
// implemented, and sprig provides no function of that name.
func TestExpandRejectsTplFunction(t *testing.T) {
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
      name: '{{ tpl "{{ .cluster }}" . }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	_, err := Expand(appSet, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not defined")
}

// TestExpandRendersMultiSourceApplications covers spec.sources, where each
// entry needs the same field-by-field rendering as a single source.
func TestExpandRendersMultiSourceApplications(t *testing.T) {
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
            revision: 2.0.0
  template:
    metadata:
      name: '{{ .cluster }}'
      namespace: '{{ .cluster }}-argocd'
    spec:
      destination:
        server: 'https://{{ .cluster }}.example.com'
        namespace: '{{ .cluster }}'
      sources:
        - repoURL: https://charts.example.com
          chart: guestbook
          targetRevision: '{{ .revision }}'
        - repoURL: https://git.example.com/repo.git
          path: 'charts/{{ .cluster }}'
          targetRevision: HEAD
`)

	apps, err := Expand(appSet, nil)
	require.NoError(t, err)
	require.Len(t, apps, 1)

	app := apps[0]
	assert.Equal(t, "dev-argocd", app.Metadata.Namespace)
	assert.Equal(t, "https://dev.example.com", app.Spec.Destination.Server)
	assert.Equal(t, "dev", app.Spec.Destination.Namespace)
	require.Len(t, app.Spec.Sources, 2)
	assert.Equal(t, "2.0.0", app.Spec.Sources[0].TargetRevision)
	assert.Equal(t, "charts/dev", app.Spec.Sources[1].Path)
}

// TestExpandReportsAFailureFromAnyField proves the first failing field stops
// the render and is reported, wherever in the Application it sits.
func TestExpandReportsAFailureFromAnyField(t *testing.T) {
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
        helm:
          valuesObject:
            nested:
              deep:
                - '{{ nosuchfunction .cluster }}'
`)

	_, err := Expand(appSet, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generator element 0")
	assert.Contains(t, err.Error(), "not defined")
}

// TestExpandLeavesNonStringValuesObjectEntriesAlone keeps numbers and booleans
// out of the template engine, which only has meaning for strings.
func TestExpandLeavesNonStringValuesObjectEntriesAlone(t *testing.T) {
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
        helm:
          valuesObject:
            replicaCount: 3
            enabled: true
            ratio: 1.5
`)

	apps, err := Expand(appSet, nil)
	require.NoError(t, err)

	values := apps[0].Spec.Source.Helm.ValuesObject
	assert.Equal(t, 3, values["replicaCount"])
	assert.Equal(t, true, values["enabled"])
	assert.InDelta(t, 1.5, values["ratio"], 0.0001)
}
