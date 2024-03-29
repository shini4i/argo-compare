package helpers

import (
	"testing"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
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
	assert.Equal(t, expectedValue, actualValue)

	// Test case 2: Check if a missing environment variable falls back to the default value
	expectedValue = "fallback"
	actualValue = GetEnv("MISSING_KEY", expectedValue)
	assert.Equal(t, expectedValue, actualValue)
}

func TestContains(t *testing.T) {
	// Test case 1: Check if an item is in a slice
	slice1 := []string{"apple", "banana", "cherry"}
	assert.True(t, Contains(slice1, "apple"))

	// Test case 2: Check if an item is not in a slice
	slice2 := []string{"apple", "banana", "cherry"}
	assert.False(t, Contains(slice2, "orange"))
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
	err := WriteToFile(fs, filePath, []byte(expectedStrippedOutput))
	assert.NoError(t, err)

	// Read the written file
	writtenData, err := afero.ReadFile(fs, filePath)
	assert.NoError(t, err)

	// Compare the written data with the test data
	assert.Equal(t, expectedStrippedOutput, string(writtenData))

	// Cleanup: Remove the written file
	err = fs.Remove(filePath)
	assert.NoError(t, err)

	// Test case 2: Check the error case (we should get an error if the file cannot be written)
	fs = afero.NewReadOnlyFs(fs)

	filePath = "../../testdata/invalid/output.txt"
	err = WriteToFile(fs, filePath, []byte(expectedStrippedOutput))
	assert.Error(t, err)
}

func TestCreateTempFile(t *testing.T) {
	t.Run("create and write successful", func(t *testing.T) {
		// Create a new in-memory filesystem
		fs := afero.NewMemMapFs()

		// Run the function to test
		file, err := CreateTempFile(fs, "test content")
		assert.NoError(t, err)

		// Check that the file contains the expected content
		content, err := afero.ReadFile(fs, file.Name())
		assert.NoError(t, err)
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
			username, password := FindHelmRepoCredentials(tt.url, repoCreds)
			assert.Equal(t, tt.expectedUser, username)
			assert.Equal(t, tt.expectedPass, password)
		})
	}
}
