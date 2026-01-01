package helpers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	helmDeploymentWithManagedLabels = `# for testing purpose we need only limited fields
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app.kubernetes.io/instance: traefik-web
    app.kubernetes.io/managed-by: Helm
    app.kubernetes.io/name: traefik
    argocd.argoproj.io/instance: traefik
    helm.sh/chart: traefik-23.0.1
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

func TestStripHelmLabels(t *testing.T) {
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "deployment.yaml")
	require.NoError(t, os.WriteFile(sourcePath, []byte(helmDeploymentWithManagedLabels), 0o644))

	fileContent, err := StripHelmLabels(sourcePath)

	assert.NoError(t, err)
	assert.Equal(t, expectedStrippedOutput, string(fileContent))

	// We want to be sure that the function returns an error if the file cannot be read
	_, err = StripHelmLabels(filepath.Join(tmpDir, "missing.yaml"))
	assert.Error(t, err)
}

func TestWriteToFile(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Test case 1: Check the successful case
	filePath := "output.txt"

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

	filePath = "invalid/output.txt"
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

func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()
	assert.Equal(t, 3, cfg.MaxAttempts)
	assert.Equal(t, 1*time.Second, cfg.InitialDelay)
	assert.Equal(t, 10*time.Second, cfg.MaxDelay)
	assert.Equal(t, 2.0, cfg.Multiplier)
}

func TestWithRetrySucceedsFirstAttempt(t *testing.T) {
	attempts := 0
	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, attempts)
}

func TestWithRetrySucceedsAfterRetries(t *testing.T) {
	attempts := 0
	cfg := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary failure")
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, attempts)
}

func TestWithRetryExhaustsAttempts(t *testing.T) {
	attempts := 0
	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	expectedErr := errors.New("persistent failure")
	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		return expectedErr
	})

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Equal(t, 3, attempts)
}

func TestWithRetryRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	cfg := RetryConfig{
		MaxAttempts:  10,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
	}

	// Cancel context after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := WithRetry(ctx, cfg, func() error {
		attempts++
		return errors.New("keep failing")
	})

	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	// Should have stopped early due to cancellation
	assert.Less(t, attempts, cfg.MaxAttempts)
}

func TestWithRetryHandlesInvalidConfig(t *testing.T) {
	attempts := 0

	// Test with MaxAttempts < 1 (should be normalized to 1)
	cfg := RetryConfig{
		MaxAttempts:  0,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   0.5, // Less than 1, should be normalized to 1
	}

	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		return errors.New("failure")
	})

	require.Error(t, err)
	assert.Equal(t, 1, attempts) // Should only attempt once
}

func TestWithRetryStopsOnPermanentError(t *testing.T) {
	attempts := 0
	cfg := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	permanentErr := WrapPermanent(errors.New("client error - do not retry"))
	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		return permanentErr
	})

	require.Error(t, err)
	assert.True(t, IsPermanent(err))
	assert.Equal(t, 1, attempts) // Should only attempt once due to permanent error
}

func TestWrapPermanentNil(t *testing.T) {
	err := WrapPermanent(nil)
	assert.Nil(t, err)
}

func TestIsPermanent(t *testing.T) {
	regularErr := errors.New("regular error")
	permanentErr := WrapPermanent(errors.New("permanent error"))

	assert.False(t, IsPermanent(nil))
	assert.False(t, IsPermanent(regularErr))
	assert.True(t, IsPermanent(permanentErr))
}

func TestPermanentErrorUnwrap(t *testing.T) {
	originalErr := errors.New("original error")
	permanentErr := WrapPermanent(originalErr)

	// Test that we can unwrap to get the original error
	var permErr *PermanentError
	require.True(t, errors.As(permanentErr, &permErr))
	assert.Equal(t, originalErr, permErr.Unwrap())
}
