package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports/portstest"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refRenderTarget wires a Target around the registry-chart-plus-ref shape with
// a recording Helm processor.
func refRenderTarget(t *testing.T, processor *recordingHelmProcessor, valueFiles []string) *Target {
	t.Helper()
	return &Target{
		CmdRunner:     portstest.NoopCmdRunner{},
		FileReader:    portstest.NoopFileReader{},
		HelmProcessor: processor,
		Log:           logger.New("ref-render-test"),
		TmpDir:        "/run",
		Type:          TargetTypeSource,
		App:           refApp(t, testOriginURL, valueFiles),
	}
}

// TestRenderAppSourcesResolvesRefValueFiles is the hand-off point: the renderer
// receives absolute paths, because a $ref entry cannot be expressed relative to
// the chart directory.
func TestRenderAppSourcesResolvesRefValueFiles(t *testing.T) {
	processor := &recordingHelmProcessor{}
	target := refRenderTarget(t, processor, []string{"values.yaml", "$values/envs/prod/values.yaml"})

	require.NoError(t, target.renderAppSources(context.Background()))

	require.Len(t, processor.renderRequests, 1, "a values-only ref source renders nothing of its own")
	assert.Equal(t, []string{
		filepath.Join("/run", "charts", TargetTypeSource, "prometheus", "values.yaml"),
		filepath.Join("/run", "refs", TargetTypeSource, "values", "envs/prod/values.yaml"),
	}, processor.renderRequests[0].ValueFiles)
}

// TestRenderAppSourcesFailsOnUnknownRef stops rather than rendering without the
// values file, which would produce a diff that silently omits an override.
func TestRenderAppSourcesFailsOnUnknownRef(t *testing.T) {
	processor := &recordingHelmProcessor{}
	target := refRenderTarget(t, processor, []string{"$typo/values.yaml"})

	err := target.renderAppSources(context.Background())

	require.ErrorIs(t, err, ErrUnknownValueFileRef)
	assert.Empty(t, processor.renderRequests)
}

// TestEnsureHelmChartsSkipsRefSource keeps a Git ref source out of the Helm
// registry pipeline: it has no chart to pull.
func TestEnsureHelmChartsSkipsRefSource(t *testing.T) {
	processor := &recordingHelmProcessor{}
	target := refRenderTarget(t, processor, []string{"$values/values.yaml"})

	require.NoError(t, target.ensureHelmCharts(context.Background()))
	require.NoError(t, target.extractCharts(context.Background()))

	require.Len(t, processor.downloadRequests, 1)
	assert.Equal(t, "prometheus", processor.downloadRequests[0].ChartName)
	assert.Equal(t, 1, processor.extractCalls, "only the chart source is extracted")
}

// TestGenerateValuesFilesSkipsRefSource guards against a stray helm block on a
// values-only ref source producing an unused inline values file.
func TestGenerateValuesFilesSkipsRefSource(t *testing.T) {
	processor := &recordingHelmProcessor{}
	target := refRenderTarget(t, processor, []string{"$values/values.yaml"})
	target.App.Spec.Sources[1].Helm.Values = "ignored: true"

	require.NoError(t, target.generateValuesFiles())

	assert.Equal(t, 0, processor.generateValuesCalls)
}

// TestPathBasedIgnoresRefSource keeps a path-based chart plus a values-only ref
// on the path pipeline; the ref source has no path of its own.
func TestPathBasedIgnoresRefSource(t *testing.T) {
	target := refRenderTarget(t, &recordingHelmProcessor{}, []string{"$values/values.yaml"})
	target.App.Spec.Sources[0].Chart = ""
	target.App.Spec.Sources[0].Path = "charts/demo"

	assert.True(t, target.PathBased())
	assert.NoError(t, target.ClassifySources())
	require.Len(t, target.pathSources(), 1, "the ref source must not be materialized as a chart")
	assert.Equal(t, "charts/demo", target.pathSources()[0].Path)
}

// TestRegistryChartWithRefIsNotPathBased pins the reporter's shape from issue
// #157: a registry chart plus a ref source stays on the registry pipeline.
func TestRegistryChartWithRefIsNotPathBased(t *testing.T) {
	target := refRenderTarget(t, &recordingHelmProcessor{}, []string{"$values/values.yaml"})

	assert.False(t, target.PathBased())
	assert.NoError(t, target.ClassifySources(), "a ref source is neither chart- nor path-based, so it cannot make the Application mixed")
}

// TestMaterializeChartPullsRegistryChart covers the anchor flow reaching a
// registry-chart Application, which it can now do when the values come from a
// ref source in this repository. Without the registry pipeline the chart
// directory would stay empty and helm would render nothing.
func TestMaterializeChartPullsRegistryChart(t *testing.T) {
	processor := &recordingHelmProcessor{}
	target := refRenderTarget(t, processor, []string{"$values/values.yaml"})
	appInstance := &App{cfg: Config{TargetBranch: "main"}, fs: afero.NewMemMapFs(), logger: logger.New("anchor-registry-test")}

	require.NoError(t, appInstance.materializeChart(context.Background(), nil, target, t.TempDir()))

	require.Len(t, processor.downloadRequests, 1, "the registry chart must be pulled")
	assert.Equal(t, "prometheus", processor.downloadRequests[0].ChartName)
	assert.Equal(t, 1, processor.extractCalls, "the pulled chart must be extracted")
}

// TestMaterializeChartKeepsPathBasedOffTheRegistry guards the existing
// behaviour: a path-based anchored chart comes from the tree, never helm pull.
func TestMaterializeChartKeepsPathBasedOffTheRegistry(t *testing.T) {
	processor := &recordingHelmProcessor{}
	target := refRenderTarget(t, processor, nil)
	target.App.Spec.Sources = target.App.Spec.Sources[:1]
	target.App.Spec.Sources[0].Chart = ""
	target.App.Spec.Sources[0].Path = "charts/demo"

	repoRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "charts/demo"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "charts/demo/Chart.yaml"), []byte("name: demo\n"), 0o644))

	appInstance := &App{cfg: Config{TargetBranch: "main"}, fs: afero.NewMemMapFs(), logger: logger.New("anchor-path-test")}

	require.NoError(t, appInstance.materializeChart(context.Background(), nil, target, repoRoot))

	assert.Empty(t, processor.downloadRequests, "a path-based chart must not trigger helm pull")
	assert.Equal(t, 0, processor.extractCalls)
}

// TestPathBasedEdgeCases covers the guards the single/multi-source unification
// folded into renderableSources, including a nil spec.source that the old code
// checked explicitly.
func TestPathBasedEdgeCases(t *testing.T) {
	cases := []struct {
		name    string
		source  *models.Source
		sources []*models.Source
		multi   bool
		want    bool
	}{
		{name: "nil single source"},
		{name: "empty sources slice", multi: true, sources: []*models.Source{}},
		{name: "only a values-only ref source", multi: true, sources: []*models.Source{{Ref: "values"}}},
		{
			name:    "nil entry beside a path source",
			multi:   true,
			sources: []*models.Source{nil, {Path: "charts/demo"}},
			want:    true,
		},
		{
			name:    "path source beside a values-only ref",
			multi:   true,
			sources: []*models.Source{{Path: "charts/demo"}, {Ref: "values"}},
			want:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := &Target{TmpDir: "/run", Type: TargetTypeSource}
			target.App.Spec.Source = tc.source
			target.App.Spec.Sources = tc.sources
			target.App.Spec.MultiSource = tc.multi

			assert.Equal(t, tc.want, target.PathBased())
		})
	}
}

// TestNilSingleSourceIsANoOp keeps the render pipeline from dereferencing a nil
// spec.source, which the removed single-source branches used to guard.
func TestNilSingleSourceIsANoOp(t *testing.T) {
	processor := &recordingHelmProcessor{}
	target := &Target{
		CmdRunner:     portstest.NoopCmdRunner{},
		FileReader:    portstest.NoopFileReader{},
		HelmProcessor: processor,
		Log:           logger.New("nil-source-test"),
		TmpDir:        "/run",
		Type:          TargetTypeSource,
	}

	assert.Empty(t, target.renderableSources())
	assert.Empty(t, target.pathSources())
	require.NoError(t, target.generateValuesFiles())
	require.NoError(t, target.ensureHelmCharts(context.Background()))
	require.NoError(t, target.extractCharts(context.Background()))
	require.NoError(t, target.renderAppSources(context.Background()))

	assert.Equal(t, 0, processor.generateValuesCalls)
	assert.Empty(t, processor.downloadRequests)
	assert.Equal(t, 0, processor.extractCalls)
	assert.Empty(t, processor.renderRequests)
}

// TestSourceWithoutValueFilesPassesNone keeps a chart relying on its own
// values.yaml from gaining a --values flag.
func TestSourceWithoutValueFilesPassesNone(t *testing.T) {
	processor := &recordingHelmProcessor{}
	target := refRenderTarget(t, processor, nil)

	require.NoError(t, target.renderAppSources(context.Background()))

	require.Len(t, processor.renderRequests, 1)
	assert.Empty(t, processor.renderRequests[0].ValueFiles)
}

// TestMaterializeChartUnknownLeg mirrors TestMaterializeRefSources_UnknownLeg for the
// chart step, which reads the leg from Target.Type as well.
func TestMaterializeChartUnknownLeg(t *testing.T) {
	target := refRenderTarget(t, &recordingHelmProcessor{}, nil)
	target.Type = "sideways"
	target.App.Spec.Sources = target.App.Spec.Sources[:1]
	target.App.Spec.Sources[0].Chart = ""
	target.App.Spec.Sources[0].Path = "charts/demo"
	appInstance := &App{cfg: Config{TargetBranch: "main"}, fs: afero.NewMemMapFs(), logger: logger.New("unknown-leg-test")}

	err := appInstance.materializeChart(context.Background(), nil, target, t.TempDir())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown render leg")
}
