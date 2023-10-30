package helpers

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/spf13/afero"
)

// GetEnv retrieves the value of an environment variable specified by the given key.
// If the environment variable is set, its value is returned.
// If the environment variable is not set, the provided fallback value is returned.
func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// Contains checks if a string `s` is present in the given string slice `slice`.
// It iterates over each item in the slice and returns true if a match is found.
// If no match is found, it returns false.
func Contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// StripHelmLabels removes the specified Helm labels from the content of a file.
// The function takes a file path as input and returns the stripped file content as a byte slice.
// It removes the labels listed in the `labels` slice using regular expressions.
// The function returns an error if there is an issue reading the file.
func StripHelmLabels(file string) ([]byte, error) {
	// list of labels to remove
	labels := []string{
		"app.kubernetes.io/managed-by",
		"helm.sh/chart",
		"chart",
		"app.kubernetes.io/version",
	}

	regex := strings.Join(labels, "|")

	// remove helm labels as they are not needed for comparison
	// it might be error-prone, as those labels are not always the same
	re := regexp.MustCompile("(?m)[\r\n]+^.*(" + regex + "):.*$")

	var fileData []byte
	var err error

	if fileData, err = os.ReadFile(file); err != nil {
		return nil, err
	}

	strippedFileData := re.ReplaceAll(fileData, []byte(""))

	return strippedFileData, nil
}

// WriteToFile writes the provided data to a file specified by the file path.
// It takes a file path and a byte slice of data as input.
// The function writes the data to the file with the specified file permissions (0644).
// It returns an error if there is an issue writing to the file.
func WriteToFile(fs afero.Fs, file string, data []byte) error {
	if err := afero.WriteFile(fs, file, data, 0644); err != nil {
		return err
	}
	return nil
}

// CreateTempFile creates a temporary file in the "/tmp" directory with a unique name
// that has the prefix "compare-" and suffix ".yaml". It then writes the provided content
// to this temporary file. The function returns a pointer to the created os.File if it
// succeeds. If the function fails at any step, it returns an error wrapped with context
// about what step of the process it failed at.
func CreateTempFile(fs afero.Fs, content string) (afero.File, error) {
	tmpFile, err := afero.TempFile(fs, "/tmp", "compare-*.yaml")
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
// If no matching credentials are found, it returns two empty strings.
func FindHelmRepoCredentials(url string, credentials []models.RepoCredentials) (string, string) {
	for _, repoCred := range credentials {
		if repoCred.Url == url {
			return repoCred.Username, repoCred.Password
		}
	}
	return "", ""
}
