package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports/portstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spf13/afero"
)

// multiSourceApp builds an Application with spec.sources, the shape every
// source-slot test needs and the one the anonymous spec struct makes verbose.
func multiSourceApp(sources ...*models.Source) models.Application {
	app := models.Application{}
	app.Metadata.Name = "app"
	app.Spec.Sources = sources
	app.Spec.MultiSource = true
	app.Spec.Destination = &models.Destination{Namespace: "demo"}
	return app
}

// fakeRepoToken stands in for a credential embedded in a repoURL.
const fakeRepoToken = "not-a-real-token"

// credentialRepoURL assembles a repoURL carrying userinfo. It is built at run
// time rather than written inline so the repository's secret scanner does not
// have to decide whether a synthetic fixture is a leak.
func credentialRepoURL() string {
	return "https://user:" + fakeRepoToken + "@git.example.com/gitops"
}

// singleSourceApp builds an Application with spec.source.
func singleSourceApp(source *models.Source) models.Application {
	app := models.Application{}
	app.Metadata.Name = "app"
	app.Spec.Source = source
	app.Spec.Destination = &models.Destination{Namespace: "demo"}
	return app
}

func TestSourceSlotIsEmptyForSingleSource(t *testing.T) {
	tgt := Target{TmpDir: "tmp", Type: TargetTypeSource, App: singleSourceApp(&models.Source{
		RepoURL: "registry.example.com", Chart: "redis", TargetRevision: "1.0.0",
	})}

	src := tgt.App.Spec.Source
	assert.Empty(t, tgt.sourceSlot(src),
		"a single-source Application has nothing to disambiguate and must keep the shorter paths")
	assert.Equal(t, filepath.Join("tmp", "charts", TargetTypeSource, "redis"), tgt.chartDirFor(src))
	assert.Equal(t, filepath.Join("tmp", "templates", TargetTypeSource), tgt.outputDirFor(src))
	// The one single-source path that did move: the inline values file used to
	// sit flat in TmpDir as "<chart>-values-<leg>.yaml".
	assert.Equal(t, filepath.Join("tmp", "values", TargetTypeSource, "redis-values.yaml"),
		tgt.inlineValuesFileFor(src))
}

func TestSourceSlotSeparatesSameChartFromDifferentRegistries(t *testing.T) {
	a := &models.Source{RepoURL: "registry-a.example.com", Chart: "redis", TargetRevision: "1.0.0"}
	b := &models.Source{RepoURL: "registry-b.example.com", Chart: "redis", TargetRevision: "1.0.0"}
	tgt := Target{TmpDir: "tmp", Type: TargetTypeSource, App: multiSourceApp(a, b)}

	assert.NotEqual(t, tgt.sourceSlot(a), tgt.sourceSlot(b))
	assert.NotEqual(t, tgt.chartDirFor(a), tgt.chartDirFor(b))
	assert.NotEqual(t, tgt.outputDirFor(a), tgt.outputDirFor(b))
	assert.NotEqual(t, tgt.inlineValuesFileFor(a), tgt.inlineValuesFileFor(b))

	// Stated literally, because every other test derives its expectation from
	// these helpers and would pass for any layout at all.
	assert.Len(t, tgt.sourceSlot(a), slotLength)
	slot := tgt.sourceSlot(a)
	assert.Equal(t, filepath.Join("tmp", "charts", TargetTypeSource, slot, "redis"), tgt.chartDirFor(a))
	assert.Equal(t, filepath.Join("tmp", "templates", TargetTypeSource, slot), tgt.outputDirFor(a))
	assert.Equal(t, filepath.Join("tmp", "values", TargetTypeSource, slot, "redis-values.yaml"),
		tgt.inlineValuesFileFor(a))
}

// TestSourceSlotMovesWithRepoURL records the accepted cost of telling two
// same-named charts apart: migrating a source to another registry changes its
// slot, so the diff reports that source's manifests as removed and re-added.
func TestSourceSlotMovesWithRepoURL(t *testing.T) {
	before := &models.Source{RepoURL: "registry-a.example.com", Chart: "redis", TargetRevision: "1.0.0"}
	after := &models.Source{RepoURL: "registry-b.example.com", Chart: "redis", TargetRevision: "1.0.0"}
	other := &models.Source{RepoURL: "registry-a.example.com", Chart: "postgres", TargetRevision: "1.0.0"}

	dstLeg := Target{TmpDir: "tmp", Type: TargetTypeDestination, App: multiSourceApp(before, other)}
	srcLeg := Target{TmpDir: "tmp", Type: TargetTypeSource, App: multiSourceApp(after, other)}

	assert.NotEqual(t, dstLeg.sourceSlot(before), srcLeg.sourceSlot(after))
	assert.Equal(t, dstLeg.sourceSlot(other), srcLeg.sourceSlot(other),
		"an untouched source must keep its slot so its manifests still diff in place")
}

// TestSourceSlotSeparatesNamespacedChartsSharingABasename covers the OCI shape
// #183 made reachable: two repository namespaces under one registry whose
// charts share a basename, and therefore share the directory the tarball
// unpacks into.
func TestSourceSlotSeparatesNamespacedChartsSharingABasename(t *testing.T) {
	a := &models.Source{RepoURL: "registry.example.com", Chart: "team-a/redis", TargetRevision: "1.0.0"}
	b := &models.Source{RepoURL: "registry.example.com", Chart: "team-b/redis", TargetRevision: "1.0.0"}
	tgt := Target{TmpDir: "tmp", Type: TargetTypeSource, App: multiSourceApp(a, b)}

	assert.NotEqual(t, tgt.sourceSlot(a), tgt.sourceSlot(b))
}

func TestSourceSlotSeparatesPathSourcesSharingABasename(t *testing.T) {
	a := &models.Source{RepoURL: "git.example.com/gitops", Path: "charts/team-a/redis"}
	b := &models.Source{RepoURL: "git.example.com/gitops", Path: "charts/team-b/redis"}
	tgt := Target{TmpDir: "tmp", Type: TargetTypeSource, App: multiSourceApp(a, b)}

	assert.NotEqual(t, tgt.sourceSlot(a), tgt.sourceSlot(b))
}

// TestSourceSlotIgnoresTargetRevision pins the property the diff depends on:
// the two comparison legs render the same source at different revisions, so a
// revision in the slot would move every manifest and report the whole chart as
// removed and re-added.
func TestSourceSlotIgnoresTargetRevision(t *testing.T) {
	src := &models.Source{RepoURL: "registry.example.com", Chart: "redis", TargetRevision: "1.0.0"}
	dst := &models.Source{RepoURL: "registry.example.com", Chart: "redis", TargetRevision: "2.0.0"}
	other := &models.Source{RepoURL: "registry.example.com", Chart: "postgres", TargetRevision: "1.0.0"}

	srcLeg := Target{TmpDir: "tmp", Type: TargetTypeSource, App: multiSourceApp(src, other)}
	dstLeg := Target{TmpDir: "tmp", Type: TargetTypeDestination, App: multiSourceApp(dst, other)}

	assert.Equal(t, srcLeg.sourceSlot(src), dstLeg.sourceSlot(dst))
}

// TestClassifySourcesRejectsIndistinguishableSources covers the residual case
// the slot cannot separate: two sources sharing repoURL, chart and path. The
// run fails instead of silently rendering one chart twice.
func TestClassifySourcesRejectsIndistinguishableSources(t *testing.T) {
	cases := []struct {
		name       string
		sources    []*models.Source
		wantRef    string
		wantAbsent string
	}{
		{
			name: "registry chart",
			sources: []*models.Source{
				{RepoURL: "registry.example.com", Chart: "redis", TargetRevision: "1.0.0"},
				{RepoURL: "registry.example.com", Chart: "redis", TargetRevision: "2.0.0"},
			},
			wantRef: "redis",
		},
		{
			name: "git path",
			sources: []*models.Source{
				{RepoURL: "git.example.com/gitops", Path: "charts/redis", TargetRevision: "main"},
				{RepoURL: "git.example.com/gitops", Path: "charts/redis", TargetRevision: "v1"},
			},
			wantRef: "charts/redis",
		},
		{
			// The message reaches CI logs and merge request comments, so a
			// token embedded in repoURL must not travel with it.
			name: "credentials in repoURL are redacted",
			sources: []*models.Source{
				{RepoURL: credentialRepoURL(), Path: "charts/redis", TargetRevision: "main"},
				{RepoURL: credentialRepoURL(), Path: "charts/redis", TargetRevision: "v1"},
			},
			wantRef:    "https://git.example.com/gitops",
			wantAbsent: fakeRepoToken,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tgt := Target{TmpDir: "tmp", Type: TargetTypeSource, App: multiSourceApp(c.sources...)}

			err := tgt.ClassifySources()
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrIndistinguishableSources)
			assert.Contains(t, err.Error(), c.wantRef)
			if c.wantAbsent != "" {
				assert.NotContains(t, err.Error(), c.wantAbsent)
			}
		})
	}
}

// TestSourceSlotSeparatesFieldsOfEqualText pins the NUL separation: a registry
// chart and a Git path of the same name under one repoURL hash identically if
// the fields are simply concatenated, and would then share a directory.
func TestSourceSlotSeparatesFieldsOfEqualText(t *testing.T) {
	chart := &models.Source{RepoURL: "reg.example.com", Chart: "redis"}
	path := &models.Source{RepoURL: "reg.example.com", Path: "redis"}
	tgt := Target{TmpDir: "tmp", Type: TargetTypeSource, App: multiSourceApp(chart, path)}

	assert.NotEqual(t, tgt.sourceSlot(chart), tgt.sourceSlot(path))
}

// TestSourceSlotIgnoresAnExplicitDefaultReleaseName pins that spelling out the
// fallback changes nothing. Helm renders under the Application name either way,
// so a slot keyed on the raw field would move every manifest of that source for
// an edit that alters no output.
func TestSourceSlotIgnoresAnExplicitDefaultReleaseName(t *testing.T) {
	implicit := &models.Source{RepoURL: "registry.example.com", Chart: "redis", TargetRevision: "1.0.0"}
	explicit := &models.Source{
		RepoURL: "registry.example.com", Chart: "redis", TargetRevision: "1.0.0",
		Helm: models.HelmSource{ReleaseName: "app"}, // the Application's own name
	}
	other := &models.Source{RepoURL: "registry.example.com", Chart: "postgres", TargetRevision: "1.0.0"}

	dstLeg := Target{TmpDir: "tmp", Type: TargetTypeDestination, App: multiSourceApp(implicit, other)}
	srcLeg := Target{TmpDir: "tmp", Type: TargetTypeSource, App: multiSourceApp(explicit, other)}

	assert.Equal(t, dstLeg.sourceSlot(implicit), srcLeg.sourceSlot(explicit))
}

// TestSourceSlotSeparatesSourcesByReleaseName covers one chart deployed twice
// under two release names. helm already nests its output under the release, so
// including it in the slot keeps a valid Application renderable rather than
// rejecting it for an extraction directory it would otherwise share.
func TestSourceSlotSeparatesSourcesByReleaseName(t *testing.T) {
	a := &models.Source{
		RepoURL: "registry.example.com", Chart: "redis", TargetRevision: "1.0.0",
		Helm: models.HelmSource{ReleaseName: "cache"},
	}
	b := &models.Source{
		RepoURL: "registry.example.com", Chart: "redis", TargetRevision: "1.0.0",
		Helm: models.HelmSource{ReleaseName: "sessions"},
	}
	tgt := Target{TmpDir: "tmp", Type: TargetTypeSource, App: multiSourceApp(a, b)}

	require.NoError(t, tgt.ClassifySources())
	assert.NotEqual(t, tgt.sourceSlot(a), tgt.sourceSlot(b))
	assert.NotEqual(t, tgt.chartDirFor(a), tgt.chartDirFor(b))
}

// TestMultiSourceSameChartNameRendersFromItsOwnDirectory is the regression
// test for #185: every path the renderer hands to Helm must be distinct per
// source, or the second extraction overwrites the first and both sources
// render from whichever chart landed last.
func TestMultiSourceSameChartNameRendersFromItsOwnDirectory(t *testing.T) {
	processor := &recordingHelmProcessor{}
	tgt := Target{
		CmdRunner:     portstest.NoopCmdRunner{},
		FileReader:    portstest.NoopFileReader{},
		HelmProcessor: processor,
		Globber:       portstest.NoopGlobber{},
		CacheDir:      "cache",
		TmpDir:        "tmp",
		Log:           logger.New("source-slot-test"),
		Type:          TargetTypeSource,
		App: multiSourceApp(
			&models.Source{
				RepoURL:        "registry-a.example.com",
				Chart:          "redis",
				TargetRevision: "1.0.0",
				Helm:           models.HelmSource{Values: "replicaCount: 1"},
			},
			&models.Source{
				RepoURL:        "registry-b.example.com",
				Chart:          "redis",
				TargetRevision: "1.0.0",
				Helm:           models.HelmSource{Values: "replicaCount: 2"},
			},
		),
	}

	require.NoError(t, tgt.ClassifySources())
	require.NoError(t, tgt.generateValuesFiles())
	require.NoError(t, tgt.extractCharts(context.Background()))
	require.NoError(t, tgt.renderAppSources(context.Background()))

	require.Len(t, processor.extractRequests, 2)
	assert.NotEqual(t, processor.extractRequests[0].ExtractDir, processor.extractRequests[1].ExtractDir,
		"each source must unpack its tarball into its own directory")

	require.Len(t, processor.renderRequests, 2)
	for i, ext := range processor.extractRequests {
		assert.Equal(t, filepath.Join(ext.ExtractDir, ext.ChartName), processor.renderRequests[i].ChartDir,
			"helm must be pointed at the directory tar unpacked the chart into")
	}
	assert.NotEqual(t, processor.renderRequests[0].ChartDir, processor.renderRequests[1].ChartDir,
		"each source must render from the chart it declared")
	assert.NotEqual(t, processor.renderRequests[0].OutputDir, processor.renderRequests[1].OutputDir,
		"rendered manifests sharing an output directory overwrite each other and vanish from the diff")
	assert.NotEqual(t, processor.renderRequests[0].InlineValuesFile, processor.renderRequests[1].InlineValuesFile,
		"inline values are per-source and must not share a file")

	require.Len(t, processor.generateValuesPaths, 2)
	assert.NotEqual(t, processor.generateValuesPaths[0], processor.generateValuesPaths[1])
	assert.Equal(t, processor.generateValuesPaths[0], processor.renderRequests[0].InlineValuesFile,
		"the renderer must look for the inline values file where it was written")
}

// TestMultiSourceSameChartNamePairsAcrossLegs closes the loop on #185 through
// Compare: both sources must survive rendering as manifests of their own, and
// each must pair with its counterpart on the other leg rather than being
// reported as one removed and one added.
func TestMultiSourceSameChartNamePairsAcrossLegs(t *testing.T) {
	tmpDir := t.TempDir()
	processor := newStubHelmProcessor(t)

	renderLegAt := func(leg, revision string) {
		t.Helper()
		tgt := Target{
			CmdRunner:     portstest.NoopCmdRunner{},
			FileReader:    portstest.NoopFileReader{},
			HelmProcessor: processor,
			Globber:       portstest.NoopGlobber{},
			CacheDir:      filepath.Join(tmpDir, "cache"),
			TmpDir:        tmpDir,
			Log:           logger.New("pairing-test"),
			Type:          leg,
			App: multiSourceApp(
				&models.Source{RepoURL: "registry-a.example.com", Chart: "redis", TargetRevision: revision},
				&models.Source{RepoURL: "registry-b.example.com", Chart: "redis", TargetRevision: revision},
			),
		}
		require.NoError(t, tgt.ClassifySources())
		require.NoError(t, tgt.extractCharts(context.Background()))
		require.NoError(t, tgt.renderAppSources(context.Background()))
	}

	renderLegAt(TargetTypeDestination, "1.0.0")
	renderLegAt(TargetTypeSource, "2.0.0")

	comparison := Compare{
		Fs:                 afero.NewOsFs(),
		Globber:            utils.CustomGlobber{},
		TmpDir:             tmpDir,
		PreserveHelmLabels: true,
	}
	result, err := comparison.Execute()
	require.NoError(t, err)

	assert.Empty(t, result.Added, "a source present on both legs must not read as added")
	assert.Empty(t, result.Removed, "a source present on both legs must not read as removed")
	require.Len(t, result.Changed, 2, "both sources must reach the diff, not just the one that landed last")
	assert.NotEqual(t, result.Changed[0].File.Name, result.Changed[1].File.Name)
}
