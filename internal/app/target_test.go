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

func (r *recordingHelmProcessor) RenderAppSource(_ context.Context, _ ports.CmdRunner, _ ports.ChartRenderRequest) error {
	r.renderCalls++
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
