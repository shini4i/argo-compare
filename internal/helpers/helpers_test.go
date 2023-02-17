package helpers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	testFile := filepath.Join(getGitRepoRoot(), "test/data/test.yaml")
	expectedContents := "apiVersion: argoproj.io/v1alpha1"

	// Test case 1: Check if a file is read successfully
	actualContents := ReadFile(testFile)
	if !strings.Contains(string(actualContents), expectedContents) {
		t.Errorf("expected file contents to contain [%s], but got [%s]", expectedContents, string(actualContents))
	}

	// Test case 2: Check if a missing file is handled properly
	missingFile := filepath.Join(getGitRepoRoot(), "test/data/missing.yaml")
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
	// Set up test environment
	testDir := "test/data"

	// Determine the root of the Git repository
	repoRoot := getGitRepoRoot()

	// Run the function being tested
	yamlFiles, err := FindYamlFiles(filepath.Join(repoRoot, testDir))
	if err != nil {
		t.Fatalf("error finding YAML files: %v", err)
	}

	// Check the result
	if len(yamlFiles) == 0 {
		t.Errorf("expected to find at least one YAML file in test directory [%s], but none were found", testDir)
	}
}

func getGitRepoRoot() string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		panic(fmt.Sprintf("failed to get git repository root: %v", err))
	}
	return strings.TrimSpace(string(out))
}
