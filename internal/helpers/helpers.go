// Package helpers provides utility functions for environment variable handling,
// file operations, Helm label stripping, and retry logic used throughout the application.
package helpers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/spf13/afero"
)

// helmLabelRegex matches Helm-managed labels that should be stripped from manifests.
// Compiled once at package initialization for performance.
var helmLabelRegex = regexp.MustCompile(`(?m)[\r\n]+^.*(app\.kubernetes\.io/managed-by|helm\.sh/chart|chart|app\.kubernetes\.io/version):.*$`)

// GetEnv retrieves the value of an environment variable specified by the given key.
// If the environment variable is set, its value is returned.
// GetEnv returns the value of the environment variable named by key or the provided fallback value when the variable is not set.
func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// StripHelmLabels removes Helm-managed labels from the content of a file.
// The function takes a file path as input and returns the stripped file content as a byte slice.
// It uses a pre-compiled regex to remove labels that change between Helm runs
// (app.kubernetes.io/managed-by, helm.sh/chart, chart, app.kubernetes.io/version).
// StripHelmLabels reads the named file and removes Helm-managed label lines (for example
// `app.kubernetes.io/managed-by`, `helm.sh/chart`, `chart`, `app.kubernetes.io/version`)
// from its content.
// It returns the modified file content as a byte slice, or an error if reading the file fails.
func StripHelmLabels(file string) ([]byte, error) {
	fileData, err := os.ReadFile(file) // #nosec G304
	if err != nil {
		return nil, err
	}

	strippedFileData := helmLabelRegex.ReplaceAll(fileData, []byte(""))

	return strippedFileData, nil
}

// WriteToFile writes the provided data to a file specified by the file path.
// It takes a file path and a byte slice of data as input.
// The function writes the data to the file with the specified file permissions (0644).
// WriteToFile writes data to the named file using the provided afero filesystem with file mode 0644.
// It returns an error if writing the file fails.
func WriteToFile(fs afero.Fs, file string, data []byte) error {
	return afero.WriteFile(fs, file, data, 0644)
}

// CreateTempFile creates a temporary file in the system's default temp directory with
// a unique name that has the prefix "compare-" and suffix ".yaml". It then writes the
// provided content to this temporary file. The function returns a pointer to the created
// os.File if it succeeds. If the function fails at any step, it returns an error wrapped
// CreateTempFile creates a temporary YAML file, writes the provided content into it, and returns the open file handle.
// The file is created with the prefix "compare-" and ".yaml" suffix in the system temporary directory.
// Returns the created afero.File on success, or an error if file creation or writing fails.
func CreateTempFile(fs afero.Fs, content string) (afero.File, error) {
	// Empty string uses the system's default temp directory (os.TempDir())
	tmpFile, err := afero.TempFile(fs, "", "compare-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file: %w", err)
	}

	if err = WriteToFile(fs, tmpFile.Name(), []byte(content)); err != nil {
		return nil, fmt.Errorf("failed to write to temporary file: %w", err)
	}

	return tmpFile, nil
}

// FindHelmRepoCredentials scans the provided array of RepoCredentials for a match to the
// provided repository URL, and returns the associated username and password.
// FindHelmRepoCredentials looks up credentials for the given repository URL.
// It returns the username and password for the matching repository; if no match is found both strings are empty.
func FindHelmRepoCredentials(url string, credentials []models.RepoCredentials) (string, string) {
	for _, repoCred := range credentials {
		if repoCred.Url == url {
			return repoCred.Username, repoCred.Password
		}
	}
	return "", ""
}

// RetryConfig holds configuration for retry operations.
type RetryConfig struct {
	// MaxAttempts is the maximum number of attempts (must be >= 1).
	MaxAttempts int
	// InitialDelay is the delay before the first retry.
	InitialDelay time.Duration
	// MaxDelay is the maximum delay between retries (caps exponential backoff).
	MaxDelay time.Duration
	// Multiplier is the factor by which the delay increases after each retry.
	Multiplier float64
}

// DefaultRetryConfig returns a RetryConfig configured with sensible defaults for retrying operations.
// The returned config sets MaxAttempts to 3, InitialDelay to 1s, MaxDelay to 10s, and Multiplier to 2.0.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Second,
		MaxDelay:     10 * time.Second,
		Multiplier:   2.0,
	}
}

// PermanentError wraps an error to signal that retry should not be attempted.
// Use WrapPermanent to create a permanent error.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string {
	return e.Err.Error()
}

func (e *PermanentError) Unwrap() error {
	return e.Err
}

// WrapPermanent wraps an error to indicate it should not be retried.
func WrapPermanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// IsPermanent reports whether err is a PermanentError indicating the operation should not be retried.
// It returns true if err is or wraps a PermanentError.
func IsPermanent(err error) bool {
	var permErr *PermanentError
	return errors.As(err, &permErr)
}

// WithRetry executes the given function with retry logic using exponential backoff.
// It respects context cancellation and returns early if the context is cancelled.
// If the function returns a PermanentError, retry is skipped and the error is returned immediately.
// WithRetry executes fn repeatedly according to cfg, applying exponential backoff and respecting context cancellation.
// 
// WithRetry normalizes cfg (ensuring at least one attempt and a multiplier of at least 1), then calls fn up to
// cfg.MaxAttempts times. It waits with an initial delay of cfg.InitialDelay and multiplies the delay by cfg.Multiplier
// between attempts, capping at cfg.MaxDelay. It returns immediately if ctx is cancelled or if fn returns a PermanentError.
// 
// On success, it returns nil. If the context is cancelled before or during attempts, it returns ctx.Err(). If all
// attempts fail (or a permanent error is returned by fn), it returns the last error produced by fn.
func WithRetry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	if cfg.Multiplier < 1 {
		cfg.Multiplier = 1
	}

	var lastErr error
	delay := cfg.InitialDelay

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		// Check context before each attempt
		if err := ctx.Err(); err != nil {
			return err
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// Don't retry permanent errors
		if IsPermanent(lastErr) {
			return lastErr
		}

		// Don't sleep after the last attempt
		if attempt == cfg.MaxAttempts {
			break
		}

		// Wait with context cancellation support
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		// Increase delay for next iteration (exponential backoff)
		delay = time.Duration(float64(delay) * cfg.Multiplier)
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}

	return lastErr
}