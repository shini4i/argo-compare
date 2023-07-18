package helpers

import (
	"errors"
	"fmt"
	"github.com/mattn/go-zglob"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/spf13/afero"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// ReadFile reads the contents of the specified file and returns them as a byte slice.
// If the file does not exist, it prints a message indicating that the file was removed in a source branch and returns nil.
// The function handles the os.ErrNotExist error to detect if the file is missing.
func ReadFile(file string) []byte {
	if readFile, err := os.ReadFile(file); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("File [%s] was removed in a source branch, skipping...\n", file)
		return nil
	} else {
		return readFile
	}
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

// FindYamlFiles finds all YAML files recursively in the specified directory path.
// It takes a directory path as input.
// The function uses the zglob package to perform a glob pattern matching with the pattern "**/*.yaml".
// It returns a slice of file paths for all found YAML files and an error if there is an issue during the search.
func FindYamlFiles(dirPath string) ([]string, error) {
	return zglob.Glob(filepath.Join(dirPath, "**", "*.yaml"))
}

// GetGitRepoRoot returns the root directory of the current Git repository.
// It takes a cmdRunner as input, which is an interface for executing shell commands.
// The function runs the "git rev-parse --show-toplevel" command to retrieve the root directory path.
// It captures the standard output and standard error streams and returns them as strings.
// If the command execution is successful, it trims the leading and trailing white spaces from the output and returns it as the repository root directory path.
// If there is an error executing the command, the function prints the error message to standard error and returns an empty string and the error.
func GetGitRepoRoot(cmdRunner utils.CmdRunner) (string, error) {
	stdout, stderr, err := cmdRunner.Run("git", "rev-parse", "--show-toplevel")
	if err != nil {
		fmt.Println(stderr)
		return "", err
	}
	return strings.TrimSpace(stdout), nil
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
