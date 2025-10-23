package app

import (
	"testing"

	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func (r *recordingHelmProcessor) DownloadHelmChart(cmdRunner ports.CmdRunner, globber ports.Globber, cacheDir, repoUrl, chartName, targetRevision string, repoCredentials []models.RepoCredentials) error {
	r.downloadCalls++
	return nil
}

func (r *recordingHelmProcessor) ExtractHelmChart(cmdRunner ports.CmdRunner, globber ports.Globber, chartName, chartVersion, chartLocation, tmpDir, targetType string) error {
	r.extractCalls++
	return nil
}

func (r *recordingHelmProcessor) RenderAppSource(cmdRunner ports.CmdRunner, releaseName, chartName, chartVersion, tmpDir, targetType, namespace string) error {
	r.renderCalls++
	return nil
}

type noopCmdRunner struct{}

func (noopCmdRunner) Run(string, ...string) (string, string, error) { return "", "", nil }

type noopFileReader struct{}

func (noopFileReader) ReadFile(string) []byte { return nil }

func TestTargetMultiSourceInvokesHelmPerSource(t *testing.T) {
	processor := &recordingHelmProcessor{}

	target := Target{
		CmdRunner:       noopCmdRunner{},
		FileReader:      noopFileReader{},
		HelmProcessor:   processor,
		CacheDir:        "cache",
		TmpDir:          "tmp",
		RepoCredentials: nil,
		Log:             logging.MustGetLogger("target-test"),
		Type:            "src",
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
	require.NoError(t, target.ensureHelmCharts())
	require.NoError(t, target.extractCharts())
	require.NoError(t, target.renderAppSources())

	assert.Equal(t, 2, processor.generateValuesCalls)
	assert.Equal(t, 2, processor.downloadCalls)
	assert.Equal(t, 2, processor.extractCalls)
	assert.Equal(t, 2, processor.renderCalls)
}
