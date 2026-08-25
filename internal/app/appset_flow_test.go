package app

import (
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func namedApp(name, revision string) models.Application {
	app := models.Application{Kind: models.KindApplication}
	app.Metadata.Name = name
	app.Spec.Source = &models.Source{
		RepoURL:        "https://charts.example.com",
		Chart:          "guestbook",
		TargetRevision: revision,
	}
	return app
}

// TestPairGeneratedApplicationsMatchesByName covers the case a plain
// Application manifest cannot produce: a change to the ApplicationSet alters
// the set of generated Applications, not only their contents.
func TestPairGeneratedApplicationsMatchesByName(t *testing.T) {
	srcApps := []models.Application{
		namedApp("kept", "2.0.0"),
		namedApp("added", "1.0.0"),
	}
	dstApps := []models.Application{
		namedApp("kept", "1.0.0"),
		namedApp("removed", "1.0.0"),
	}

	pairs := pairGeneratedApplications(srcApps, dstApps)
	require.Len(t, pairs, 3)

	assert.Equal(t, "kept", pairs[0].name)
	require.NotNil(t, pairs[0].src)
	require.NotNil(t, pairs[0].dst)
	assert.Equal(t, "2.0.0", pairs[0].src.Spec.Source.TargetRevision)
	assert.Equal(t, "1.0.0", pairs[0].dst.Spec.Source.TargetRevision)

	assert.Equal(t, "added", pairs[1].name)
	require.NotNil(t, pairs[1].src)
	assert.Nil(t, pairs[1].dst)

	assert.Equal(t, "removed", pairs[2].name)
	assert.Nil(t, pairs[2].src)
	require.NotNil(t, pairs[2].dst)
}

// TestPairGeneratedApplicationsPointsAtDistinctElements guards against the
// loop-variable aliasing that would give every pair the same Application.
func TestPairGeneratedApplicationsPointsAtDistinctElements(t *testing.T) {
	srcApps := []models.Application{namedApp("one", "1.0.0"), namedApp("two", "2.0.0")}

	pairs := pairGeneratedApplications(srcApps, nil)
	require.Len(t, pairs, 2)
	assert.Equal(t, "1.0.0", pairs[0].src.Spec.Source.TargetRevision)
	assert.Equal(t, "2.0.0", pairs[1].src.Spec.Source.TargetRevision)
}

func TestPairGeneratedApplicationsHandlesEmptySides(t *testing.T) {
	assert.Empty(t, pairGeneratedApplications(nil, nil))

	added := pairGeneratedApplications([]models.Application{namedApp("only", "1.0.0")}, nil)
	require.Len(t, added, 1)
	assert.Nil(t, added[0].dst)

	removed := pairGeneratedApplications(nil, []models.Application{namedApp("only", "1.0.0")})
	require.Len(t, removed, 1)
	assert.Nil(t, removed[0].src)
}

func TestParseApplicationSetContent(t *testing.T) {
	appSet, err := parseApplicationSetContent([]byte(`
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
`))

	require.NoError(t, err)
	assert.Equal(t, "guestbook", appSet.Metadata.Name)

	_, err = parseApplicationSetContent([]byte("kind: Application\nmetadata:\n  name: demo\n"))
	assert.ErrorIs(t, err, models.ErrNotApplicationSet)
}

// TestGeneratedLabelNamesTheApplication guards the diff and comment header from
// labelling every Application of one ApplicationSet with the same path.
func TestGeneratedLabelNamesTheApplication(t *testing.T) {
	assert.Equal(t, "apps/guestbook.yaml [dev-guestbook]", generatedLabel("apps/guestbook.yaml", "dev-guestbook"))
}

// TestPairGeneratedApplicationsPreservesOrder pins the docstring's guarantee:
// source order first, then target-only names in target order. Map iteration
// order would make this flaky if dstByName drove the sequence.
func TestPairGeneratedApplicationsPreservesOrder(t *testing.T) {
	srcApps := []models.Application{namedApp("b", "1.0.0"), namedApp("a", "1.0.0")}
	dstApps := []models.Application{namedApp("z", "1.0.0"), namedApp("a", "1.0.0"), namedApp("y", "1.0.0")}

	for i := 0; i < 20; i++ {
		pairs := pairGeneratedApplications(srcApps, dstApps)
		names := make([]string, 0, len(pairs))
		for _, pair := range pairs {
			names = append(names, pair.name)
		}
		require.Equal(t, []string{"b", "a", "z", "y"}, names)
	}
}

func TestParseApplicationSetContentRejectsNonMapping(t *testing.T) {
	_, err := parseApplicationSetContent([]byte("- a\n- b\n"))
	require.Error(t, err)
	assert.NotErrorIs(t, err, models.ErrNotApplicationSet)
}

// TestParseApplicationSetTreatsMissingFileAsEmpty documents the FileReader
// contract: os.ErrNotExist becomes (nil, nil), so a missing manifest surfaces
// as ErrEmptyFile rather than a read error.
func TestParseApplicationSetTreatsMissingFileAsEmpty(t *testing.T) {
	_, err := parseApplicationSet(utils.OsFileReader{}, "/nonexistent/appset.yaml")
	assert.ErrorIs(t, err, models.ErrEmptyFile)
}
