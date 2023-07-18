package helpers

import (
	"errors"
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"path/filepath"
	"strings"
	"testing"
)

const (
	expectedStrippedOutput = `# for testing purpose we need only limited fields
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app.kubernetes.io/instance: traefik-web
    app.kubernetes.io/name: traefik
    argocd.argoproj.io/instance: traefik
  name: traefik
  namespace: web
`
)

func TestGetEnv(t *testing.T) {
	// Test case 1: Check if an existing environment variable is retrieved
	expectedValue := "test value"
	t.Setenv("TEST_KEY", expectedValue)

	actualValue := GetEnv("TEST_KEY", "fallback")
	if actualValue != expectedValue {
		t.Errorf("expected value to be [%s], but got [%s]", expectedValue, actualValue)
	}

	// Test case 2: Check if a missing environment variable falls back to the default value
	expectedValue = "fallback"
	actualValue = GetEnv("MISSING_KEY", expectedValue)
	if actualValue != expectedValue {
		t.Errorf("expected value to be [%s], but got [%s]", expectedValue, actualValue)
	}
}

func TestReadFile(t *testing.T) {
	// Set up test environment
	repoRoot, err := GetGitRepoRoot(&utils.RealCmdRunner{})
	if err != nil {
		t.Fatalf("error finding git repo root: %v", err)
	}

	testFile := filepath.Join(repoRoot, "testdata/test.yaml")
	expectedContents := "apiVersion: argoproj.io/v1alpha1"

	// Test case 1: Check if a file is read successfully
	actualContents := ReadFile(testFile)
	if !strings.Contains(string(actualContents), expectedContents) {
		t.Errorf("expected file contents to contain [%s], but got [%s]", expectedContents, string(actualContents))
	}

	// Test case 2: Check if a missing file is handled properly
	missingFile := filepath.Join(repoRoot, "testdata/missing.yaml")
	actualContents = ReadFile(missingFile)
	assert.Nilf(t, actualContents, "expected file contents to be nil, but got [%s]", string(actualContents))
}

func TestContains(t *testing.T) {
	// Test case 1: Check if an item is in a slice
	slice1 := []string{"apple", "banana", "cherry"}
	if !Contains(slice1, "banana") {
		t.Errorf("expected to find 'banana' in slice, but didn't")
	}

	// Test case 2: Check if an item is not in a slice
	slice2 := []string{"apple", "banana", "cherry"}
	if Contains(slice2, "orange") {
		t.Errorf("expected not to find 'orange' in slice, but did")
	}
}

func TestFindYamlFiles(t *testing.T) {
	testDir := "testdata"
	repoRoot, err := GetGitRepoRoot(&utils.RealCmdRunner{})
	if err != nil {
		t.Fatalf("error finding git repo root: %v", err)
	}

	yamlFiles, err := FindYamlFiles(filepath.Join(repoRoot, testDir))
	if err != nil {
		t.Fatalf("error finding YAML files: %v", err)
	}

	if len(yamlFiles) == 0 {
		t.Errorf("expected to find at least one YAML file in test directory [%s], but none were found", testDir)
	}
}

func TestStripHelmLabels(t *testing.T) {
	// Call the function to strip Helm labels
	fileContent, err := StripHelmLabels("../../testdata/dynamic/deployment.yaml")

	assert.NoError(t, err)
	assert.Equal(t, expectedStrippedOutput, string(fileContent))

	// We want to be sure that the function returns an error if the file cannot be read
	_, err = StripHelmLabels("../../testdata/invalid.yaml")
	assert.Error(t, err)
}

func TestWriteToFile(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Test case 1: Check the successful case
	filePath := "../../testdata/dynamic/output.txt"

	// Call the function to write data to file
	if err := WriteToFile(fs, filePath, []byte(expectedStrippedOutput)); err != nil {
		t.Fatalf("WriteToFile returned an error: %s", err)
	}

	// Read the written file
	writtenData, err := afero.ReadFile(fs, filePath)
	if err != nil {
		t.Fatalf("Failed to read the written file: %s", err)
	}

	// Compare the written data with the test data
	assert.Equal(t, expectedStrippedOutput, string(writtenData))

	// Cleanup: Remove the written file
	if err := fs.Remove(filePath); err != nil {
		t.Fatalf("Failed to remove the written file: %s", err)
	}

	// Test case 2: Check the error case (we should get an error if the file cannot be written)
	fs = afero.NewReadOnlyFs(fs)

	filePath = "../../testdata/invalid/output.txt"
	err = WriteToFile(fs, filePath, []byte(expectedStrippedOutput))
	assert.Error(t, err)
}

func TestGetGitRepoRoot(t *testing.T) {
	// Test case 1: Check if the git repo root is found
	repoRoot, err := GetGitRepoRoot(&utils.RealCmdRunner{})
	if err != nil {
		t.Fatalf("error finding git repo root: %v", err)
	}
	assert.NotEmptyf(t, repoRoot, "expected repo root to be non-empty, but got [%s]", repoRoot)

	// Test case 2: Check if the git repo root could not be found
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	mockCmdRunner.EXPECT().Run("git", "rev-parse", "--show-toplevel").Return("", "", errors.New("git not found"))

	repoRoot, err = GetGitRepoRoot(mockCmdRunner)
	assert.Emptyf(t, repoRoot, "expected repo root to be empty, but got [%s]", repoRoot)
	assert.Errorf(t, err, "expected error to be returned, but got nil")
}

func TestCreateTempFile(t *testing.T) {
	t.Run("create and write successful", func(t *testing.T) {
		// Create a new in-memory filesystem
		fs := afero.NewMemMapFs()

		// Run the function to test
		file, err := CreateTempFile(fs, "test content")
		if err != nil {
			t.Fatalf("Failed to create temporary file: %s", err)
		}

		// Check that the file contains the expected content
		content, err := afero.ReadFile(fs, file.Name())
		if err != nil {
			t.Fatalf("Failed to read temporary file: %s", err)
		}

		assert.Equal(t, "test content", string(content))
	})

	t.Run("failed to create file", func(t *testing.T) {
		// Create a read-only in-memory filesystem
		fs := afero.NewReadOnlyFs(afero.NewMemMapFs())

		// Run the function to test
		_, err := CreateTempFile(fs, "test content")

		// assert error to contain the expected message
		assert.Contains(t, err.Error(), "failed to create temporary file")
	})
}
