package utils

import (
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"os"
	"os/exec"
	"testing"
)

const (
	testsDir = "../../../testdata/disposable"
)

func TestGenerateValuesFile(t *testing.T) {
	helmChartProcessor := RealHelmChartProcessor{}

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
	err = helmChartProcessor.GenerateValuesFile(chartName, tmpDir, targetType, values)
	assert.NoError(t, err, "expected no error, got %v", err)

	// Read the generated file
	generatedValues, err := os.ReadFile(tmpDir + "/" + chartName + "-values-" + targetType + ".yaml")
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, values, string(generatedValues))

	// Test case 2: Error when creating the file
	err = helmChartProcessor.GenerateValuesFile(chartName, "/non/existing/path", targetType, values)
	assert.Error(t, err, "expected error, got nil")
}

func TestDownloadHelmChart(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helmChartProcessor := RealHelmChartProcessor{Log: logging.MustGetLogger("test")}

	// Create the mocks
	mockGlobber := mocks.NewMockGlobber(ctrl)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	// Test case 1: chart exists
	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{testsDir + "/ingress-nginx-3.34.0.tgz"}, nil)
	err := helmChartProcessor.DownloadHelmChart(mockCmdRunner,
		mockGlobber,
		testsDir+"/cache",
		"https://chart.example.com",
		"ingress-nginx",
		"3.34.0",
		[]models.RepoCredentials{},
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
	err = helmChartProcessor.DownloadHelmChart(mockCmdRunner,
		mockGlobber,
		testsDir+"/cache",
		"https://chart.example.com",
		"ingress-nginx",
		"3.34.0",
		[]models.RepoCredentials{},
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
	err = helmChartProcessor.DownloadHelmChart(mockCmdRunner,
		mockGlobber,
		testsDir+"/cache",
		"https://chart.example.com",
		"ingress-nginx",
		"3.34.0",
		[]models.RepoCredentials{},
	)
	assert.ErrorIsf(t, err, FailedToDownloadChart, "expected error %v, got %v", FailedToDownloadChart, err)
}

func TestFindHelmRepoCredentials(t *testing.T) {
	repoCreds := []models.RepoCredentials{
		{
			Url:      "https://charts.example.com",
			Username: "user",
			Password: "pass",
		},
		{
			Url:      "https://charts.test.com",
			Username: "testuser",
			Password: "testpass",
		},
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
			username, password := helpers.FindHelmRepoCredentials(tt.url, repoCreds)
			assert.Equal(t, tt.expectedUser, username)
			assert.Equal(t, tt.expectedPass, password)
		})
	}
}
