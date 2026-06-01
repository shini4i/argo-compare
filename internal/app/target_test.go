package app

import (
	"context"
	"errors"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"

	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/shini4i/argo-compare/internal/ports/portstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shini4i/argo-compare/internal/models"
)

type recordingHelmProcessor struct {
	generateValuesCalls int
	downloadCalls       int
	extractCalls        int
	renderCalls         int
	renderRequests      []ports.ChartRenderRequest
}

func (r *recordingHelmProcessor) GenerateValuesFile(chartName, tmpDir, targetType, values string, valuesObject map[string]interface{}) error {
	r.generateValuesCalls++
	return nil
}

func (r *recordingHelmProcessor) DownloadHelmChart(_ context.Context, _ ports.HelmDeps, _ ports.ChartDownloadRequest) error {
	r.downloadCalls++
	return nil
}

func (r *recordingHelmProcessor) ExtractHelmChart(_ context.Context, _ ports.HelmDeps, _ ports.ChartExtractRequest) error {
	r.extractCalls++
	return nil
}

func (r *recordingHelmProcessor) RenderAppSource(_ context.Context, _ ports.CmdRunner, req ports.ChartRenderRequest) error {
	r.renderCalls++
	r.renderRequests = append(r.renderRequests, req)
	return nil
}

func (r *recordingHelmProcessor) BuildChartDependencies(_ context.Context, _ ports.HelmDeps, _, _ string) error {
	return nil
}

func TestTargetParseReturnsErrorFromFileReader(t *testing.T) {
	sentinel := errors.New("permission denied")

	target := Target{
		CmdRunner:  portstest.NoopCmdRunner{},
		FileReader: portstest.ErrFileReader{Err: sentinel},
		Log:        logger.New("target-test"),
		File:       "/some/app.yaml",
		Type:       TargetTypeSource,
	}

	err := target.parse()
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel, "original error must be reachable via errors.Is")
	assert.Contains(t, err.Error(), "/some/app.yaml", "error must include the file path")
}

func TestTargetMultiSourceInvokesHelmPerSource(t *testing.T) {
	processor := &recordingHelmProcessor{}

	target := Target{
		CmdRunner:           portstest.NoopCmdRunner{},
		FileReader:          portstest.NoopFileReader{},
		HelmProcessor:       processor,
		Globber:             portstest.NoopGlobber{},
		CacheDir:            "cache",
		TmpDir:              "tmp",
		CredentialProviders: nil,
		Log:                 logger.New("target-test"),
		Type:                TargetTypeSource,
		App: models.Application{
			Spec: struct {
				Source      *models.Source      `yaml:"source"`
				Sources     []*models.Source    `yaml:"sources"`
				MultiSource bool                `yaml:"-"`
				Destination *models.Destination `yaml:"destination"`
			}{
				Sources: []*models.Source{
					{
						RepoURL:        "repoA",
						Chart:          "chartA",
						TargetRevision: "1.0.0",
						Helm: struct {
							ReleaseName  string                 `yaml:"releaseName,omitempty"`
							Values       string                 `yaml:"values,omitempty"`
							ValueFiles   []string               `yaml:"valueFiles,omitempty"`
							ValuesObject map[string]interface{} `yaml:"valuesObject,omitempty"`
						}{
							ReleaseName: "releaseA",
							Values:      "replicaCount: 1",
						},
					},
					{
						RepoURL:        "repoB",
						Chart:          "chartB",
						TargetRevision: "2.0.0",
						Helm: struct {
							ReleaseName  string                 `yaml:"releaseName,omitempty"`
							Values       string                 `yaml:"values,omitempty"`
							ValueFiles   []string               `yaml:"valueFiles,omitempty"`
							ValuesObject map[string]interface{} `yaml:"valuesObject,omitempty"`
						}{
							ValuesObject: map[string]interface{}{"replicaCount": 3},
						},
					},
				},
				MultiSource: true,
				Destination: &models.Destination{Namespace: "demo"},
			},
		},
	}

	require.NoError(t, target.generateValuesFiles())
	require.NoError(t, target.ensureHelmCharts(context.Background()))
	require.NoError(t, target.extractCharts(context.Background()))
	require.NoError(t, target.renderAppSources(context.Background()))

	assert.Equal(t, 2, processor.generateValuesCalls)
	assert.Equal(t, 2, processor.downloadCalls)
	assert.Equal(t, 2, processor.extractCalls)
	assert.Equal(t, 2, processor.renderCalls)
}

// TestTargetSkipsValuesGenerationWhenInlineEmpty verifies that sources without
// inline values (no helm.values and no helm.valuesObject) skip
// GenerateValuesFile entirely. Applications that rely solely on
// helm.valueFiles or on the chart's own defaults must not crash on the
// "either 'values' or 'valuesObject' must be provided" guard.
func TestTargetSkipsValuesGenerationWhenInlineEmpty(t *testing.T) {
	processor := &recordingHelmProcessor{}

	target := Target{
		HelmProcessor: processor,
		Log:           logger.New("target-test"),
		Type:          TargetTypeSource,
		App: models.Application{
			Spec: struct {
				Source      *models.Source      `yaml:"source"`
				Sources     []*models.Source    `yaml:"sources"`
				MultiSource bool                `yaml:"-"`
				Destination *models.Destination `yaml:"destination"`
			}{
				Source: &models.Source{
					RepoURL: "ssh://git@example.com/repo.git",
					Path:    "cms-2/staging",
					Helm: struct {
						ReleaseName  string                 `yaml:"releaseName,omitempty"`
						Values       string                 `yaml:"values,omitempty"`
						ValueFiles   []string               `yaml:"valueFiles,omitempty"`
						ValuesObject map[string]interface{} `yaml:"valuesObject,omitempty"`
					}{
						ValueFiles: []string{"values.yaml", "environment.yaml"},
					},
				},
			},
		},
	}

	require.NoError(t, target.generateValuesFiles())
	assert.Equal(t, 0, processor.generateValuesCalls, "no inline values means no values file must be generated")
}

// TestTargetPropagatesValueFiles verifies that helm.valueFiles entries flow
// through to ChartRenderRequest.ValueFiles in declared order. This is the
// hand-off point between target.go and helm_chart_processor.go for the
// valueFiles flag.
func TestTargetPropagatesValueFiles(t *testing.T) {
	processor := &recordingHelmProcessor{}

	target := Target{
		CmdRunner:     portstest.NoopCmdRunner{},
		HelmProcessor: processor,
		Log:           logger.New("target-test"),
		Type:          TargetTypeSource,
		App: models.Application{
			Spec: struct {
				Source      *models.Source      `yaml:"source"`
				Sources     []*models.Source    `yaml:"sources"`
				MultiSource bool                `yaml:"-"`
				Destination *models.Destination `yaml:"destination"`
			}{
				Source: &models.Source{
					RepoURL: "ssh://git@example.com/repo.git",
					Path:    "cms-2/staging",
					Helm: struct {
						ReleaseName  string                 `yaml:"releaseName,omitempty"`
						Values       string                 `yaml:"values,omitempty"`
						ValueFiles   []string               `yaml:"valueFiles,omitempty"`
						ValuesObject map[string]interface{} `yaml:"valuesObject,omitempty"`
					}{
						ValueFiles: []string{"values.yaml", "environment.yaml", "worker.yaml"},
					},
				},
				Destination: &models.Destination{Namespace: "demo"},
			},
		},
	}

	require.NoError(t, target.renderAppSources(context.Background()))
	require.Len(t, processor.renderRequests, 1)
	assert.Equal(t, []string{"values.yaml", "environment.yaml", "worker.yaml"}, processor.renderRequests[0].ValueFiles)
}

// TestTargetMultiSourceSkipsValuesGenerationForEmptySources verifies that in a
// multi-source Application, only sources with inline values trigger
// GenerateValuesFile. Sources that rely solely on valueFiles or chart defaults
// must not cause the "either values or valuesObject must be provided" error.
func TestTargetMultiSourceSkipsValuesGenerationForEmptySources(t *testing.T) {
	processor := &recordingHelmProcessor{}

	target := Target{
		HelmProcessor: processor,
		Log:           logger.New("target-test"),
		Type:          TargetTypeSource,
		App: models.Application{
			Spec: struct {
				Source      *models.Source      `yaml:"source"`
				Sources     []*models.Source    `yaml:"sources"`
				MultiSource bool                `yaml:"-"`
				Destination *models.Destination `yaml:"destination"`
			}{
				Sources: []*models.Source{
					{
						Chart: "chartA",
						Helm: struct {
							ReleaseName  string                 `yaml:"releaseName,omitempty"`
							Values       string                 `yaml:"values,omitempty"`
							ValueFiles   []string               `yaml:"valueFiles,omitempty"`
							ValuesObject map[string]interface{} `yaml:"valuesObject,omitempty"`
						}{
							Values: "replicaCount: 2",
						},
					},
					{
						Chart: "chartB",
						Helm: struct {
							ReleaseName  string                 `yaml:"releaseName,omitempty"`
							Values       string                 `yaml:"values,omitempty"`
							ValueFiles   []string               `yaml:"valueFiles,omitempty"`
							ValuesObject map[string]interface{} `yaml:"valuesObject,omitempty"`
						}{
							ValueFiles: []string{"extra.yaml"},
						},
					},
				},
				MultiSource: true,
			},
		},
	}

	require.NoError(t, target.generateValuesFiles())
	assert.Equal(t, 1, processor.generateValuesCalls, "only the source with inline values must generate a values file")
}

// TestTargetMultiSourcePropagatesValueFiles verifies that each source's
// helm.valueFiles flow through to the corresponding ChartRenderRequest so that
// the renderer receives the correct per-source file list.
func TestTargetMultiSourcePropagatesValueFiles(t *testing.T) {
	processor := &recordingHelmProcessor{}

	target := Target{
		CmdRunner:     portstest.NoopCmdRunner{},
		HelmProcessor: processor,
		Log:           logger.New("target-test"),
		Type:          TargetTypeSource,
		App: models.Application{
			Spec: struct {
				Source      *models.Source      `yaml:"source"`
				Sources     []*models.Source    `yaml:"sources"`
				MultiSource bool                `yaml:"-"`
				Destination *models.Destination `yaml:"destination"`
			}{
				Sources: []*models.Source{
					{
						Chart: "chartA",
						Helm: struct {
							ReleaseName  string                 `yaml:"releaseName,omitempty"`
							Values       string                 `yaml:"values,omitempty"`
							ValueFiles   []string               `yaml:"valueFiles,omitempty"`
							ValuesObject map[string]interface{} `yaml:"valuesObject,omitempty"`
						}{
							ValueFiles: []string{"a-values.yaml"},
						},
					},
					{
						Chart: "chartB",
						Helm: struct {
							ReleaseName  string                 `yaml:"releaseName,omitempty"`
							Values       string                 `yaml:"values,omitempty"`
							ValueFiles   []string               `yaml:"valueFiles,omitempty"`
							ValuesObject map[string]interface{} `yaml:"valuesObject,omitempty"`
						}{
							ValueFiles: []string{"b-values.yaml", "b-env.yaml"},
						},
					},
				},
				MultiSource: true,
				Destination: &models.Destination{Namespace: "demo"},
			},
		},
	}

	require.NoError(t, target.renderAppSources(context.Background()))
	require.Len(t, processor.renderRequests, 2)
	assert.Equal(t, []string{"a-values.yaml"}, processor.renderRequests[0].ValueFiles)
	assert.Equal(t, []string{"b-values.yaml", "b-env.yaml"}, processor.renderRequests[1].ValueFiles)
}
