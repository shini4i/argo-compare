package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	interfaces "github.com/shini4i/argo-compare/cmd/argo-compare/interfaces"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/spf13/afero"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/models"
)

type GitRepo struct {
	CmdRunner    interfaces.CmdRunner
	FsType       afero.Fs
	changedFiles []string
	invalidFiles []string
}

var (
	gitFileDoesNotExist = errors.New("file does not exist in target branch")
)

func printChangeFile(files []string) {
	log.Debug("===> Found the following changed files:")
	for _, file := range files {
		if file != "" {
			log.Debugf("▶ %s", file)
		}
	}
}

func (g *GitRepo) sortChangedFiles(fileReader interfaces.FileReader, files []string) {
	for _, file := range files {
		if filepath.Ext(file) == ".yaml" {
			switch isApp, err := checkIfApp(g.CmdRunner, fileReader, file); {
			case errors.Is(err, models.NotApplicationError):
				log.Debugf("Skipping non-application file [%s]", file)
			case errors.Is(err, models.UnsupportedAppConfigurationError):
				log.Warningf("Skipping unsupported application configuration [%s]", file)
			case errors.Is(err, models.EmptyFileError):
				log.Debugf("Skipping empty file [%s]", file)
			case err != nil:
				log.Errorf("Error checking if [%s] is an Application: %s", file, err)
				g.invalidFiles = append(g.invalidFiles, file)
			case isApp:
				g.changedFiles = append(g.changedFiles, file)
			}
		}
	}

	if len(g.changedFiles) > 0 {
		log.Info("===> Found the following changed Application files")
		for _, file := range g.changedFiles {
			log.Infof("▶ %s", file)
		}
	}
}

func (g *GitRepo) getChangedFiles(fileReader interfaces.FileReader) ([]string, error) {
	if stdout, stderr, err := g.CmdRunner.Run("git", "--no-pager", "diff", "--name-only", targetBranch); err != nil {
		log.Errorf("Error running git command: %s", stderr)
		return nil, err
	} else {
		foundFiles := strings.Split(stdout, "\n")
		printChangeFile(foundFiles)
		g.sortChangedFiles(fileReader, foundFiles)
	}
	return g.changedFiles, nil
}

func (g *GitRepo) getChangedFileContent(targetBranch string, targetFile string) (models.Application, error) {
	log.Debugf("Getting content of %s from %s", targetFile, targetBranch)

	stdout, stderr, err := g.CmdRunner.Run("git", "--no-pager", "show", targetBranch+":"+targetFile)
	if err != nil {
		if strings.Contains(stderr, "exists on disk, but not in") {
			color.Yellow("The requested file does not exist in target branch, assuming it is a new Application")
			if !printAddedManifests {
				return models.Application{}, gitFileDoesNotExist
			}
		} else {
			return models.Application{}, fmt.Errorf("failed to get the content of the file: %w", err)
		}
	}

	tmpFile, err := helpers.CreateTempFile(g.FsType, stdout)
	if err != nil {
		return models.Application{}, err
	}

	defer func(file afero.File) {
		if err := afero.Fs.Remove(g.FsType, file.Name()); err != nil {
			log.Errorf("Failed to remove temporary file [%s]: %s", file.Name(), err)
		}
	}(tmpFile)

	target := Target{CmdRunner: g.CmdRunner, FileReader: utils.OsFileReader{}, File: tmpFile.Name()}
	if err := target.parse(); err != nil {
		return models.Application{}, fmt.Errorf("failed to parse the application: %w", err)
	}

	return target.App, nil
}

func checkIfApp(cmdRunner interfaces.CmdRunner, fileReader interfaces.FileReader, file string) (bool, error) {
	log.Debugf("===> Checking if [%s] is an Application", cyan(file))

	target := Target{CmdRunner: cmdRunner, FileReader: fileReader, File: file}

	if err := target.parse(); err != nil {
		return false, err
	}
	return true, nil
}

// GetGitRepoRoot returns the root directory of the current Git repository.
// It takes a cmdRunner as input, which is an interface for executing shell commands.
// The function runs the "git rev-parse --show-toplevel" command to retrieve the root directory path.
// It captures the standard output and standard error streams and returns them as strings.
// If the command execution is successful, it trims the leading and trailing white spaces from the output and returns it as the repository root directory path.
// If there is an error executing the command, the function prints the error message to standard error and returns an empty string and the error.
func GetGitRepoRoot(cmdRunner interfaces.CmdRunner) (string, error) {
	stdout, stderr, err := cmdRunner.Run("git", "rev-parse", "--show-toplevel")
	if err != nil {
		fmt.Println(stderr)
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// ReadFile reads the contents of the specified file and returns them as a byte slice.
// If the file does not exist, it prints a message indicating that the file was removed in a source branch and returns nil.
// The function handles the os.ErrNotExist error to detect if the file is missing.
func ReadFile(file string) []byte {
	if readFile, err := os.ReadFile(file); errors.Is(err, os.ErrNotExist) {
		return nil
	} else {
		return readFile
	} // #nosec G304
}
