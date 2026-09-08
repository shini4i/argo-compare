package app

import (
	"testing"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registryChartApp builds a generated Application rendering a registry chart,
// optionally with a values-only ref source at refRepoURL.
func registryChartApp(t *testing.T, refRepoURL string) *models.Application {
	t.Helper()
	app := &models.Application{Kind: models.KindApplication}
	app.Metadata.Name = "demo"
	chart := &models.Source{RepoURL: "https://charts.example.com", Chart: "guestbook", TargetRevision: "1.0.0"}
	if refRepoURL == "" {
		app.Spec.Sources = []*models.Source{chart}
	} else {
		chart.Helm.ValueFiles = []string{"$values/envs/prod/values.yaml"}
		app.Spec.Sources = []*models.Source{chart, {RepoURL: refRepoURL, TargetRevision: "main", Ref: "values"}}
	}
	require.NoError(t, app.Validate())
	return app
}

func TestAnchoredLegSkipReason_RegistryChartAlone(t *testing.T) {
	reason, err := anchoredLegSkipReason(registryChartApp(t, ""), testOriginURL)

	require.NoError(t, err)
	assert.Contains(t, reason, "registry chart")
}

// TestAnchoredLegSkipReason_RegistryChartWithLocalRef is the case from issue
// #157: the chart is external but its values live here, so a change to this
// repository does change the render.
func TestAnchoredLegSkipReason_RegistryChartWithLocalRef(t *testing.T) {
	reason, err := anchoredLegSkipReason(registryChartApp(t, testOriginURL), testOriginURL)

	require.NoError(t, err)
	assert.Empty(t, reason, "a ref source pointing at this repository makes the Application comparable")
}

// TestAnchoredLegSkipReason_RegistryChartWithRemoteRef stays skipped: neither
// the chart nor the values come from this repository.
func TestAnchoredLegSkipReason_RegistryChartWithRemoteRef(t *testing.T) {
	reason, err := anchoredLegSkipReason(registryChartApp(t, "https://git.example.com/org/other.git"), testOriginURL)

	require.NoError(t, err)
	assert.NotEmpty(t, reason)
}

// TestAnchoredLegSkipReason_PathSourceInAnotherRepo keeps the existing guard:
// this repository's tree cannot render another repository's chart.
func TestAnchoredLegSkipReason_PathSourceInAnotherRepo(t *testing.T) {
	app := &models.Application{Kind: models.KindApplication}
	app.Metadata.Name = "demo"
	app.Spec.Sources = []*models.Source{{RepoURL: "https://git.example.com/org/other.git", Path: "charts/demo"}}
	require.NoError(t, app.Validate())

	reason, err := anchoredLegSkipReason(app, testOriginURL)

	require.NoError(t, err)
	assert.Contains(t, reason, "not this one")
}

// TestProcessAnchoredApplicationAcceptsRegistryChartWithLocalRef pins the same
// rule for a directly anchored Application as for a generated one: a registry
// chart whose values come from this repository is renderable, so the anchor
// must not reject it as "not path-based".
func TestProcessAnchoredApplicationAcceptsRegistryChartWithLocalRef(t *testing.T) {
	target := Target{App: *registryChartApp(t, testOriginURL)}

	require.NoError(t, target.ClassifySources())
	assert.False(t, target.PathBased())

	localRef, err := refsThisRepo(&target, testOriginURL)

	require.NoError(t, err)
	assert.True(t, localRef, "the anchor gate must treat this as renderable from the local tree")
}

// TestAnchoredLegSkipReasonSurfacesRefError keeps a misconfigured ref from being
// reported as an unaffected registry chart, which sends the user looking in the
// wrong place.
func TestAnchoredLegSkipReasonSurfacesRefError(t *testing.T) {
	app := registryChartApp(t, testOriginURL)
	app.Spec.Sources[0].Helm.ValueFiles = []string{"$typo/values.yaml"}

	_, err := anchoredLegSkipReason(app, testOriginURL)

	require.ErrorIs(t, err, ErrUnknownValueFileRef)
}
