package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

const (
	testsDir = "../../testdata/disposable"
)

func TestTarget_generateValuesFiles(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create an instance of the mock HelmChartsProcessor
	mockHelmValuesGenerator := mocks.NewMockHelmChartsProcessor(ctrl)

	app := Target{
		CmdRunner:  &utils.RealCmdRunner{},
		FileReader: &utils.OsFileReader{},
		File:       appFile,
	}

	err := app.parse()
	assert.NoError(t, err)

	// Test case 1: Successful values file generation with single source Application
	mockHelmValuesGenerator.EXPECT().GenerateValuesFile("ingress-nginx", gomock.Any(), gomock.Any(), gomock.Any(), nil).Return(nil)

	err = app.generateValuesFiles(mockHelmValuesGenerator)
	assert.NoError(t, err)

	// Test case 2: Successful values file generation with multiple source Applications
	app2 := Target{
		CmdRunner:  &utils.RealCmdRunner{},
		FileReader: &utils.OsFileReader{},
		File:       "testdata/test2.yaml",
	}

	err = app2.parse()
	assert.NoError(t, err)

	mockHelmValuesGenerator.EXPECT().GenerateValuesFile("kubed", gomock.Any(), gomock.Any(), gomock.Any(), nil).Return(nil)
	mockHelmValuesGenerator.EXPECT().GenerateValuesFile("sealed-secrets", gomock.Any(), gomock.Any(), gomock.Any(), nil).Return(nil)

	err = app2.generateValuesFiles(mockHelmValuesGenerator)
	assert.NoError(t, err)

	// Test case 3: Failed values file generation with single source Application
	mockHelmValuesGenerator.EXPECT().GenerateValuesFile("ingress-nginx", gomock.Any(), gomock.Any(), gomock.Any(), nil).Return(errors.New("some unexpected error"))
	err = app.generateValuesFiles(mockHelmValuesGenerator)
	assert.ErrorContains(t, err, "some unexpected error")

	// Test case 4: Failed values file generation with multiple source Applications
	mockHelmValuesGenerator.EXPECT().GenerateValuesFile("kubed", gomock.Any(), gomock.Any(), gomock.Any(), nil).Return(errors.New("multiple source apps error"))
	err = app2.generateValuesFiles(mockHelmValuesGenerator)
	assert.ErrorContains(t, err, "multiple source apps error")
}

func TestTarget_ensureHelmCharts(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create an instance of the mock HelmChartProcessor
	mockHelmChartProcessor := mocks.NewMockHelmChartsProcessor(ctrl)

	// Test case 1: Single source chart download success
	app := Target{
		CmdRunner:  &utils.RealCmdRunner{},
		FileReader: &utils.OsFileReader{},
		File:       appFile,
	}
	err := app.parse()
	assert.NoError(t, err)

	mockHelmChartProcessor.EXPECT().DownloadHelmChart(app.CmdRunner, utils.CustomGlobber{}, cacheDir, app.App.Spec.Source.RepoURL, app.App.Spec.Source.Chart, app.App.Spec.Source.TargetRevision, repoCredentials).Return(nil)

	err = app.ensureHelmCharts(mockHelmChartProcessor)
	assert.NoError(t, err)

	// Test case 2: Multiple source chart downloads success
	app2 := Target{
		CmdRunner:  &utils.RealCmdRunner{},
		FileReader: &utils.OsFileReader{},
		File:       "testdata/test2.yaml",
	}
	err = app2.parse()
	assert.NoError(t, err)

	for _, source := range app2.App.Spec.Sources {
		mockHelmChartProcessor.EXPECT().DownloadHelmChart(app2.CmdRunner, utils.CustomGlobber{}, cacheDir, source.RepoURL, source.Chart, source.TargetRevision, repoCredentials).Return(nil)
	}

	err = app2.ensureHelmCharts(mockHelmChartProcessor)
	assert.NoError(t, err)

	// Test case 3: Single source chart download failure
	mockHelmChartProcessor.EXPECT().DownloadHelmChart(app.CmdRunner, utils.CustomGlobber{}, cacheDir, app.App.Spec.Source.RepoURL, app.App.Spec.Source.Chart, app.App.Spec.Source.TargetRevision, repoCredentials).Return(errors.New("some download error"))

	err = app.ensureHelmCharts(mockHelmChartProcessor)
	assert.ErrorContains(t, err, "some download error")

	// Test case 4: Multiple source chart downloads failure
	mockHelmChartProcessor.EXPECT().DownloadHelmChart(app2.CmdRunner, utils.CustomGlobber{}, cacheDir, app2.App.Spec.Sources[0].RepoURL, app2.App.Spec.Sources[0].Chart, app2.App.Spec.Sources[0].TargetRevision, repoCredentials).Return(errors.New("multiple download error"))

	err = app2.ensureHelmCharts(mockHelmChartProcessor)
	assert.ErrorContains(t, err, "multiple download error")
}

func TestTarget_extractCharts(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create an instance of the mock HelmChartsProcessor
	mockHelmChartProcessor := mocks.NewMockHelmChartsProcessor(ctrl)

	// Test case 1: Single source chart extraction success
	app := Target{
		CmdRunner:  &utils.RealCmdRunner{},
		FileReader: &utils.OsFileReader{},
		File:       appFile,
	}
	err := app.parse()
	assert.NoError(t, err)

	mockHelmChartProcessor.EXPECT().ExtractHelmChart(app.CmdRunner, utils.CustomGlobber{}, app.App.Spec.Source.Chart, app.App.Spec.Source.TargetRevision, fmt.Sprintf("%s/%s", cacheDir, app.App.Spec.Source.RepoURL), tmpDir, app.Type).Return(nil)

	err = app.extractCharts(mockHelmChartProcessor)
	assert.NoError(t, err)

	// Test case 2: Multiple source chart extractions success
	app2 := Target{
		CmdRunner:  &utils.RealCmdRunner{},
		FileReader: &utils.OsFileReader{},
		File:       "testdata/test2.yaml",
	}
	err = app2.parse()
	assert.NoError(t, err)

	for _, source := range app2.App.Spec.Sources {
		mockHelmChartProcessor.EXPECT().ExtractHelmChart(app2.CmdRunner, utils.CustomGlobber{}, source.Chart, source.TargetRevision, fmt.Sprintf("%s/%s", cacheDir, source.RepoURL), tmpDir, app2.Type).Return(nil)
	}

	err = app2.extractCharts(mockHelmChartProcessor)
	assert.NoError(t, err)

	// Test case 3: Single source chart extraction failure
	mockHelmChartProcessor.EXPECT().ExtractHelmChart(app.CmdRunner, utils.CustomGlobber{}, app.App.Spec.Source.Chart, app.App.Spec.Source.TargetRevision, fmt.Sprintf("%s/%s", cacheDir, app.App.Spec.Source.RepoURL), tmpDir, app.Type).Return(errors.New("some extraction error"))

	err = app.extractCharts(mockHelmChartProcessor)
	assert.ErrorContains(t, err, "some extraction error")

	// Test case 4: Multiple source chart extractions failure
	mockHelmChartProcessor.EXPECT().ExtractHelmChart(app2.CmdRunner, utils.CustomGlobber{}, app2.App.Spec.Sources[0].Chart, app2.App.Spec.Sources[0].TargetRevision, fmt.Sprintf("%s/%s", cacheDir, app2.App.Spec.Sources[0].RepoURL), tmpDir, app2.Type).Return(errors.New("multiple extraction error"))

	err = app2.extractCharts(mockHelmChartProcessor)
	assert.ErrorContains(t, err, "multiple extraction error")
}

func TestTarget_renderAppSources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create a mock HelmChartsProcessor
	mockHelmChartProcessor := mocks.NewMockHelmChartsProcessor(ctrl)

	// Test case 1: Single source rendering success
	app := Target{
		CmdRunner:  &utils.RealCmdRunner{},
		FileReader: &utils.OsFileReader{},
		File:       appFile,
	}
	err := app.parse()
	assert.NoError(t, err)

	mockHelmChartProcessor.EXPECT().RenderAppSource(app.CmdRunner, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	err = app.renderAppSources(mockHelmChartProcessor)
	assert.NoError(t, err)

	// Test case 2: Multiple source rendering success
	app2 := Target{
		CmdRunner:  &utils.RealCmdRunner{},
		FileReader: &utils.OsFileReader{},
		File:       "testdata/test2.yaml",
	}
	err = app2.parse()
	assert.NoError(t, err)

	for _, source := range app2.App.Spec.Sources {
		mockHelmChartProcessor.EXPECT().RenderAppSource(app2.CmdRunner, gomock.Any(), source.Chart, source.TargetRevision, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	}

	err = app2.renderAppSources(mockHelmChartProcessor)
	assert.NoError(t, err)

	// Test case 3: Single source rendering failure
	mockHelmChartProcessor.EXPECT().RenderAppSource(app.CmdRunner, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("some rendering error"))

	err = app.renderAppSources(mockHelmChartProcessor)
	assert.ErrorContains(t, err, "some rendering error")

	// Test case 4: Multiple source rendering failure
	mockHelmChartProcessor.EXPECT().RenderAppSource(app2.CmdRunner, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("multiple rendering error"))

	err = app2.renderAppSources(mockHelmChartProcessor)
	assert.ErrorContains(t, err, "multiple rendering error")
}
