package app

import (
	"context"
	"errors"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/spf13/afero"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/shini4i/argo-compare/internal/ports/portstest"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingHelmProcessor fails the one pipeline step named by the caller and records the rest,
// so a test can pin which step's error renderLeg propagates.
type failingHelmProcessor struct {
	recordingHelmProcessor
	generateValuesErr error
	downloadErr       error
}

func (f *failingHelmProcessor) GenerateValuesFile(chartName, tmpDir, targetType, values string, valuesObject map[string]interface{}) error {
	if f.generateValuesErr != nil {
		return f.generateValuesErr
	}
	return f.recordingHelmProcessor.GenerateValuesFile(chartName, tmpDir, targetType, values, valuesObject)
}

func (f *failingHelmProcessor) DownloadHelmChart(ctx context.Context, deps ports.HelmDeps, req ports.ChartDownloadRequest) error {
	if f.downloadErr != nil {
		return f.downloadErr
	}
	return f.recordingHelmProcessor.DownloadHelmChart(ctx, deps, req)
}

// errorLegApp builds an App wired for a leg that is expected to fail before any real I/O.
func errorLegApp(scope string) *App {
	return &App{cfg: Config{TargetBranch: "main"}, fs: afero.NewMemMapFs(), logger: logger.New(scope)}
}

// TestRenderLegStopsOnMixedMultiSource pins that a manifest rejected by ClassifySources never
// reaches chart materialization: rendering half of a mixed Application would diff against a
// chart the Application does not actually use.
func TestRenderLegStopsOnMixedMultiSource(t *testing.T) {
	processor := &recordingHelmProcessor{}
	target := refRenderTarget(t, processor, nil)
	target.App.Spec.MultiSource = true
	target.App.Spec.Sources = []*models.Source{
		{Chart: "prometheus", RepoURL: "https://charts.example.com"},
		{Path: "charts/demo", RepoURL: testOriginURL},
	}

	err := errorLegApp("mixed-leg-test").renderLeg(context.Background(), nil, target, t.TempDir(), nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMixedMultiSource)
	assert.Empty(t, processor.downloadRequests, "a rejected Application must not pull a chart")
}

// TestRenderLegPropagatesInlineValuesFailure pins that a failure writing helm.values to disk
// stops the leg. Rendering on would silently drop the overrides and report a clean diff.
func TestRenderLegPropagatesInlineValuesFailure(t *testing.T) {
	sentinel := errors.New("disk full")
	processor := &failingHelmProcessor{generateValuesErr: sentinel}
	target := refRenderTarget(t, &processor.recordingHelmProcessor, nil)
	target.HelmProcessor = processor
	target.App.Spec.Sources[0].Helm.Values = "replicaCount: 2"

	err := errorLegApp("values-leg-test").renderLeg(context.Background(), nil, target, t.TempDir(), nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Equal(t, 1, processor.downloadCalls, "values are written only after the chart is materialized")
	assert.Equal(t, 0, processor.renderCalls, "helm template must not run without the values file")
}

// TestMaterializeChartPropagatesDownloadFailure pins that a registry chart which cannot be
// pulled fails the leg rather than extracting an absent tarball.
func TestMaterializeChartPropagatesDownloadFailure(t *testing.T) {
	sentinel := errors.New("registry unreachable")
	processor := &failingHelmProcessor{downloadErr: sentinel}
	target := refRenderTarget(t, &processor.recordingHelmProcessor, []string{"$values/values.yaml"})
	target.HelmProcessor = processor

	err := errorLegApp("download-leg-test").materializeChart(context.Background(), nil, target, t.TempDir())

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Equal(t, 0, processor.extractCalls, "a chart that was not pulled must not be extracted")
}

// TestMaterializeChartWrapsRepoRootFailure pins the message for a path-based source rendered
// with no repoRoot from a directory outside any repository, which is otherwise reported as a
// bare go-git error naming neither the cause nor the leg.
func TestMaterializeChartWrapsRepoRootFailure(t *testing.T) {
	target := refRenderTarget(t, &recordingHelmProcessor{}, nil)
	target.App.Spec.Sources = target.App.Spec.Sources[:1]
	target.App.Spec.Sources[0].Chart = ""
	target.App.Spec.Sources[0].Path = "charts/demo"

	t.Chdir(t.TempDir())

	err := errorLegApp("repo-root-leg-test").materializeChart(context.Background(), nil, target, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve repo root for path-based source")
	assert.Contains(t, err.Error(), "no git repository found", "the wrap must not discard what actually failed")
}

// TestMaterializeChartPropagatesMergeBaseFailure pins that the destination leg fails when the
// target branch cannot be resolved, instead of comparing against an empty tree.
func TestMaterializeChartPropagatesMergeBaseFailure(t *testing.T) {
	repoInstance, _ := buildGitRepo(t, true)
	target := refRenderTarget(t, &recordingHelmProcessor{}, nil)
	target.Type = TargetTypeDestination
	target.App.Spec.Sources = target.App.Spec.Sources[:1]
	target.App.Spec.Sources[0].Chart = ""
	target.App.Spec.Sources[0].Path = "charts/demo"

	appInstance := errorLegApp("merge-base-leg-test")
	appInstance.cfg.TargetBranch = "no-such-branch"
	err := appInstance.materializeChart(context.Background(), repoInstance, target, t.TempDir())

	require.Error(t, err)
	assert.ErrorIs(t, err, plumbing.ErrReferenceNotFound)
	assert.Contains(t, err.Error(), "failed to resolve target branch")
	assert.Contains(t, err.Error(), "no-such-branch")
}

// TestProcessFileStopsOnUnreadableManifest pins that the source leg fails when its manifest
// cannot be read, rather than rendering the zero Application and reporting every resource as
// removed.
func TestProcessFileStopsOnUnreadableManifest(t *testing.T) {
	sentinel := errors.New("permission denied")
	appInstance := &App{
		fileReader: portstest.ErrFileReader{Err: sentinel},
		logger:     logger.New("process-file-test"),
	}

	err := appInstance.processFile(context.Background(), nil, "apps/demo.yaml", TargetTypeSource,
		models.Application{}, t.TempDir(), map[string]ports.ValidationResult{})

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "apps/demo.yaml")
}

// TestMaterializeRefSourcesForLegWrapsRepoRootFailure pins the message for a "$ref" values file
// resolved with no repoRoot outside any repository, which go-git otherwise reports without
// naming the ref sources as the reason a repo root was needed.
func TestMaterializeRefSourcesForLegWrapsRepoRootFailure(t *testing.T) {
	target := refRenderTarget(t, &recordingHelmProcessor{}, []string{"$values/values.yaml"})

	t.Chdir(t.TempDir())

	err := errorLegApp("ref-repo-root-test").
		materializeRefSourcesForLeg(context.Background(), nil, target, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve repo root for $ref values files")
	assert.Contains(t, err.Error(), "no git repository found", "the wrap must not discard what actually failed")
}
