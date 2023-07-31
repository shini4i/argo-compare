package main

import (
	"errors"
	"fmt"
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"os"
	"os/exec"
	"testing"
)

const (
	testsDir = "../../testdata/disposable"
)

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

func TestFindHelmRepoCredentials(t *testing.T) {
	repoCreds := []RepoCredentials{
		{Url: "https://charts.example.com", Username: "user", Password: "pass"},
		{Url: "https://charts.test.com", Username: "testuser", Password: "testpass"},
	}

	tests := []struct {
		name         string
		url          string
		expectedUser string
		expectedPass string
	}{
		{
			name:         "Credentials Found",
			url:          "https://charts.example.com",
			expectedUser: "user",
			expectedPass: "pass",
		},
		{
			name:         "Credentials Not Found",
			url:          "https://charts.notfound.com",
			expectedUser: "",
			expectedPass: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			username, password := findHelmRepoCredentials(tt.url, repoCreds)
			assert.Equal(t, tt.expectedUser, username)
			assert.Equal(t, tt.expectedPass, password)
		})
	}
}

func TestTarget_generateValuesFiles(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create an instance of the mock HelmValuesGenerator
	mockHelmValuesGenerator := mocks.NewMockHelmValuesGenerator(ctrl)

	app := Target{
		CmdRunner:  &utils.RealCmdRunner{},
		FileReader: &utils.OsFileReader{},
		File:       appFile,
	}

	err := app.parse()
	assert.NoError(t, err)

	// Test case 1: Successful values file generation with single source Application
	mockHelmValuesGenerator.EXPECT().GenerateValuesFile("ingress-nginx", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

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

	mockHelmValuesGenerator.EXPECT().GenerateValuesFile("kubed", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockHelmValuesGenerator.EXPECT().GenerateValuesFile("sealed-secrets", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	err = app2.generateValuesFiles(mockHelmValuesGenerator)
	assert.NoError(t, err)

	// Test case 3: Failed values file generation with single source Application
	mockHelmValuesGenerator.EXPECT().GenerateValuesFile("ingress-nginx", gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("some unexpected error"))
	err = app.generateValuesFiles(mockHelmValuesGenerator)
	assert.ErrorContains(t, err, "some unexpected error")

	// Test case 4: Failed values file generation with multiple source Applications
	mockHelmValuesGenerator.EXPECT().GenerateValuesFile("kubed", gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("multiple source apps error"))
	err = app2.generateValuesFiles(mockHelmValuesGenerator)
	assert.ErrorContains(t, err, "multiple source apps error")
}
