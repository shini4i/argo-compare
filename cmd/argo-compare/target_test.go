package main

import (
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"os"
	"os/exec"
	"testing"
)

func TestGenerateValuesFile(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("../../testdata/dynamic", "test-")
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

	// Call the function to test
	generateValuesFile(chartName, tmpDir, targetType, values)

	// Read the generated file
	generatedValues, err := os.ReadFile(tmpDir + "/" + chartName + "-values-" + targetType + ".yaml")
	if err != nil {
		t.Fatal(err)
	}

	// Verify the contents
	assert.Equal(t, values, string(generatedValues))
}

func TestDownloadHelmChart(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create the mocks
	mockGlobber := mocks.NewMockGlobber(ctrl)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	// Test case 1: chart exists
	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{"testdata/dynamic/ingress-nginx-3.34.0.tgz"}, nil)
	err := downloadHelmChart(mockCmdRunner,
		mockGlobber,
		"testdata/dynamic/cache",
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
		"testdata/dynamic/cache",
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
		"testdata/dynamic/cache",
		"https://chart.example.com",
		"ingress-nginx",
		"3.34.0",
	)

	assert.ErrorIsf(t, err, failedToDownloadChart, "expected error %v, got %v", failedToDownloadChart, err)
}
