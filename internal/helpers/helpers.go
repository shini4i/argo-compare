package helpers

import (
	"errors"
	"fmt"
	"github.com/mattn/go-zglob"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func ReadFile(file string) []byte {
	if readFile, err := os.ReadFile(file); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("File [%s] was removed in a source branch, skipping...\n", file)
		return nil
	} else {
		return readFile
	}
}

func Contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func StripHelmLabels(file string) ([]byte, error) {
	// list of labels to remove
	labels := []string{
		"app.kubernetes.io/managed-by",
		"helm.sh/chart",
		"chart",
		"app.kubernetes.io/version",
	}

	regex := fmt.Sprintf(`%s`, strings.Join(labels, "|"))

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

func WriteToFile(file string, data []byte) error {
	if err := os.WriteFile(file, data, 0644); err != nil {
		return err
	}
	return nil
}

func FindYamlFiles(dirPath string) ([]string, error) {
	return zglob.Glob(filepath.Join(dirPath, "**", "*.yaml"))
}

func GetGitRepoRoot(cmdRunner utils.CmdRunner) (string, error) {
	stdout, stderr, err := cmdRunner.Run("git", "rev-parse", "--show-toplevel")
	if err != nil {
		fmt.Println(stderr)
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}
