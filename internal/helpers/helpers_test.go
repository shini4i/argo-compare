package helpers

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindYamlFiles(t *testing.T) {
	// Set up test environment
	testDir := "test/data"

	// Determine the root of the Git repository
	repoRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("error determining Git repository root: %v", err)
	}
	gitRootDir := strings.TrimSpace(string(repoRoot))

	// Run the function being tested
	yamlFiles, err := FindYamlFiles(filepath.Join(gitRootDir, testDir))
	if err != nil {
		t.Fatalf("error finding YAML files: %v", err)
	}

	// Check the result
	if len(yamlFiles) == 0 {
		t.Errorf("expected to find at least one YAML file in test directory [%s], but none were found", testDir)
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
