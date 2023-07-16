package main

import (
	"errors"
	"fmt"
	"github.com/fatih/color"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/spf13/afero"
	"path/filepath"
	"strings"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/models"
)

type GitRepo struct {
	CmdRunner    utils.CmdRunner
	FsType       afero.Fs
	changedFiles []string
	invalidFiles []string
}

var (
	invalidFileError    = errors.New("invalid yaml file")
	gitFileDoesNotExist = errors.New("file does not exist in target branch")
)

func checkFile(cmdRunner utils.CmdRunner, fileReader utils.FileReader, file string) (bool, error) {
	if _, err := checkIfApp(cmdRunner, fileReader, file); err != nil {
		if errors.Is(err, models.NotApplicationError) {
			log.Debugf("Skipping non-application file [%s]", file)
		} else if errors.Is(err, models.UnsupportedAppConfigurationError) {
			log.Warningf("Skipping unsupported application configuration [%s]", file)
		} else if errors.Is(err, models.EmptyFileError) {
			log.Debugf("Skipping empty file [%s]", file)
		}
		return false, invalidFileError
	}
	return true, nil
}

func printChangeFile(files []string) {
	log.Debug("===> Found the following changed files:")
	for _, file := range files {
		if file != "" {
			log.Debugf("▶ %s", file)
		}
	}
}

func (g *GitRepo) sortChangedFiles(fileReader utils.FileReader, files []string) {
	for _, file := range files {
		if filepath.Ext(file) == ".yaml" {
			if _, err := checkFile(g.CmdRunner, fileReader, file); errors.Is(err, invalidFileError) {
				g.invalidFiles = append(g.invalidFiles, file)
			} else {
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

func (g *GitRepo) getChangedFiles(fileReader utils.FileReader) ([]string, error) {
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
		return models.Application{}, fmt.Errorf("failed to create temporary file: %w", err)
	}

	defer func(file afero.File) {
		err := afero.Fs.Remove(g.FsType, file.Name())
		if err != nil {
			log.Error(fmt.Errorf("failed to remove temporary file: %w", err))
		}
	}(tmpFile)

	target := Target{CmdRunner: g.CmdRunner, FileReader: utils.OsFileReader{}, File: tmpFile.Name()}
	if err := target.parse(); err != nil {
		return models.Application{}, fmt.Errorf("failed to parse the application: %w", err)
	}

	return target.App, nil
}

func checkIfApp(cmdRunner utils.CmdRunner, fileReader utils.FileReader, file string) (bool, error) {
	log.Debugf("===> Checking if [%s] is an Application", cyan(file))

	target := Target{CmdRunner: cmdRunner, FileReader: fileReader, File: file}

	if err := target.parse(); err != nil {
		return false, err
	}
	return true, nil
}
