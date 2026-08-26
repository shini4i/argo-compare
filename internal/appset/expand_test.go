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

	apps, err := Expand(appSet)
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

	apps, err := Expand(appSet)
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

	_, err := Expand(appSet)
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

	_, err := Expand(appSet)
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

	_, err := Expand(appSet)
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

	_, err := Expand(appSet)
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

	apps, err := Expand(appSet)
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

			_, err := Expand(appSet)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not defined")
		})
	}
}

func TestExpandReportsInvalidRenderedYAML(t *testing.T) {
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

	_, err := Expand(appSet)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode generated Application")
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

	_, err := Expand(appSet)
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

	apps, err := Expand(appSet)
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

	_, err := Expand(appSet)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generator element 1")
}
