package main

import (
	"errors"
	"fmt"
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"os"
	"os/exec"
	"testing"
)

const (
	testsDir = "../../testdata/disposable"
)

func TestGenerateValuesFile(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp(testsDir, "test-")
	if err != nil {
		t.Fatal(err)
	}

	// Delete the directory after the test finishes
	defer func(path string) {
		err := os.RemoveAll(path)
		if err != nil {
			t.Fatal(err)
		}
	}(tmpDir)

	chartName := "ingress-nginx"
	targetType := "src"
	values := "fullnameOverride: ingress-nginx\ncontroller:\n  kind: DaemonSet\n  service:\n    externalTrafficPolicy: Local\n    annotations:\n      fancyAnnotation: false\n"

	// Test case 1: Everything works as expected
	err = generateValuesFile(chartName, tmpDir, targetType, values)
	assert.NoError(t, err, "expected no error, got %v", err)

	// Read the generated file
	generatedValues, err := os.ReadFile(tmpDir + "/" + chartName + "-values-" + targetType + ".yaml")
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, values, string(generatedValues))

	// Test case 2: Error when creating the file
	err = generateValuesFile(chartName, "/non/existing/path", targetType, values)
	assert.Error(t, err, "expected error, got nil")
}

func TestDownloadHelmChart(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create the mocks
	mockGlobber := mocks.NewMockGlobber(ctrl)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	// Test case 1: chart exists
	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{testsDir + "/ingress-nginx-3.34.0.tgz"}, nil)
	err := downloadHelmChart(mockCmdRunner,
		mockGlobber,
		testsDir+"/cache",
		"https://chart.example.com",
		"ingress-nginx",
		"3.34.0",
	)
	assert.NoError(t, err, "expected no error, got %v", err)

	// Test case 2: chart does not exist, and successfully downloaded
	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{}, nil)
	mockCmdRunner.EXPECT().Run("helm",
		"pull",
		"--destination", gomock.Any(),
		"--username", gomock.Any(),
		"--password", gomock.Any(),
		"--repo", gomock.Any(),
		gomock.Any(),
		"--version", gomock.Any()).Return("", "", nil)
	err = downloadHelmChart(mockCmdRunner,
		mockGlobber,
		testsDir+"/cache",
		"https://chart.example.com",
		"ingress-nginx",
		"3.34.0",
	)
	assert.NoError(t, err, "expected no error, got %v", err)

	// Test case 3: chart does not exist, and failed to download
	osErr := &exec.ExitError{
		ProcessState: &os.ProcessState{},
	}
	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{}, nil)
	mockCmdRunner.EXPECT().Run("helm",
		"pull",
		"--destination", gomock.Any(),
		"--username", gomock.Any(),
		"--password", gomock.Any(),
		"--repo", gomock.Any(),
		gomock.Any(),
		"--version", gomock.Any()).Return("", "dummy error message", osErr)
	err = downloadHelmChart(mockCmdRunner,
		mockGlobber,
		testsDir+"/cache",
		"https://chart.example.com",
		"ingress-nginx",
		"3.34.0",
	)
	assert.ErrorIsf(t, err, failedToDownloadChart, "expected error %v, got %v", failedToDownloadChart, err)
}

func TestExtractHelmChart(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create the mocks
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	mockGlobber := mocks.NewMockGlobber(ctrl)

	// Set up the expected behavior for the mocks

	// Test case 1: Single chart file found
	expectedChartFileName := testsDir + "/charts/ingress-nginx/ingress-nginx-3.34.0.tgz"
	expectedChartLocation := testsDir + "/cache"
	expectedTmpDir := testsDir + "/tmp"
	expectedTargetType := "target"

	// Mock the behavior of the globber
	mockGlobber.EXPECT().Glob(testsDir+"/cache/ingress-nginx-3.34.0*.tgz").Return([]string{expectedChartFileName}, nil)

	// Mock the behavior of the cmdRunner
	mockCmdRunner.EXPECT().Run("tar",
		"xf",
		expectedChartFileName,
		"-C",
		fmt.Sprintf("%s/charts/%s", expectedTmpDir, expectedTargetType),
	).Return("", "", nil)

	err := extractHelmChart(mockCmdRunner, mockGlobber, "ingress-nginx", "3.34.0", expectedChartLocation, expectedTmpDir, expectedTargetType)

	assert.NoError(t, err, "expected no error, got %v", err)

	// Test case 2: Multiple chart files found, error expected
	expectedChartFilesNames := []string{testsDir + "/charts/sonarqube/sonarqube-4.0.0+315.tgz",
		testsDir + "/charts/sonarqube/sonarqube-4.0.0+316.tgz"}

	mockGlobber.EXPECT().Glob(testsDir+"/cache/sonarqube-4.0.0*.tgz").Return(expectedChartFilesNames, nil)

	err = extractHelmChart(mockCmdRunner, mockGlobber, "sonarqube", "4.0.0", expectedChartLocation, expectedTmpDir, expectedTargetType)
	assert.Error(t, err, "expected error, got %v", err)

	// Test case 3: Chart file found, but failed to extract
	mockGlobber.EXPECT().Glob(testsDir+"/cache/ingress-nginx-3.34.0*.tgz").Return([]string{expectedChartFileName}, nil)
	mockCmdRunner.EXPECT().Run("tar",
		"xf",
		expectedChartFileName,
		"-C",
		fmt.Sprintf("%s/charts/%s", expectedTmpDir, expectedTargetType),
	).Return("", "some unexpected error", errors.New("some unexpected error"))

	err = extractHelmChart(mockCmdRunner, mockGlobber, "ingress-nginx", "3.34.0", expectedChartLocation, expectedTmpDir, expectedTargetType)
	assert.Error(t, err, "expected error, got %v", err)

	// Test case 4: zglob failed to run
	mockGlobber.EXPECT().Glob(testsDir+"/cache/ingress-nginx-3.34.0*.tgz").Return([]string{}, os.ErrPermission)

	err = extractHelmChart(mockCmdRunner, mockGlobber, "ingress-nginx", "3.34.0", expectedChartLocation, expectedTmpDir, expectedTargetType)
	assert.Error(t, err, "expected error, got %v", err)

	// Test case 5: Failed to find chart file
	mockGlobber.EXPECT().Glob(testsDir+"/cache/ingress-nginx-3.34.0*.tgz").Return([]string{}, nil)

	err = extractHelmChart(mockCmdRunner, mockGlobber, "ingress-nginx", "3.34.0", expectedChartLocation, expectedTmpDir, expectedTargetType)
	assert.Error(t, err, "expected error, got %v", err)
}

func TestRenderAppSource(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create an instance of the mock CmdRunner
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	releaseName := "my-release"
	chartName := "my-chart"
	chartVersion := "1.2.3"
	tmpDir := testsDir + "/tmp"
	targetType := "src"

	// Test case 1: Successful render
	mockCmdRunner.EXPECT().Run("helm",
		"template",
		"--release-name", gomock.Any(),
		gomock.Any(),
		"--output-dir", gomock.Any(),
		"--values", gomock.Any(),
		"--values", gomock.Any()).Return("", "", nil)

	// Call the function under test
	err := renderAppSource(mockCmdRunner, releaseName, chartName, chartVersion, tmpDir, targetType)
	assert.NoError(t, err, "expected no error, got %v", err)

	// Test case 2: Failed render
	osErr := &exec.ExitError{
		ProcessState: &os.ProcessState{},
	}
	mockCmdRunner.EXPECT().Run("helm",
		"template",
		"--release-name", gomock.Any(),
		gomock.Any(),
		"--output-dir", gomock.Any(),
		"--values", gomock.Any(),
		"--values", gomock.Any()).Return("", "", osErr)

	err = renderAppSource(mockCmdRunner, releaseName, chartName, chartVersion, tmpDir, targetType)
	assert.Errorf(t, err, "expected error, got %v", err)
}
