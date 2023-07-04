package helpers

import (
	"github.com/stretchr/testify/assert"
	"os"
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
	err := os.Setenv("TEST_KEY", expectedValue)
	if err != nil {
		t.Fatalf("error setting test environment variable: %v", err)
	}
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
	testFile := filepath.Join(GetGitRepoRoot(), "testdata/test.yaml")
	expectedContents := "apiVersion: argoproj.io/v1alpha1"

	// Test case 1: Check if a file is read successfully
	actualContents := ReadFile(testFile)
	if !strings.Contains(string(actualContents), expectedContents) {
		t.Errorf("expected file contents to contain [%s], but got [%s]", expectedContents, string(actualContents))
	}

	// Test case 2: Check if a missing file is handled properly
	missingFile := filepath.Join(GetGitRepoRoot(), "testdata/missing.yaml")
	actualContents = ReadFile(missingFile)
	if actualContents != nil {
		t.Errorf("expected file contents to be nil, but got [%s]", string(actualContents))
	}
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
	repoRoot := GetGitRepoRoot()

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
	// Prepare test data
	filePath := "../../testdata/dynamic/output.txt"

	// Call the function to write data to file
	if err := WriteToFile(filePath, []byte(expectedStrippedOutput)); err != nil {
		t.Fatalf("WriteToFile returned an error: %s", err)
	}

	// Read the written file
	writtenData, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read the written file: %s", err)
	}

	// Compare the written data with the test data
	if string(writtenData) != expectedStrippedOutput {
		t.Errorf("Written data does not match the test data")
	}

	// Cleanup: Remove the written file
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("Failed to remove the written file: %s", err)
	}
}
