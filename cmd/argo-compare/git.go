package main

import (
	"errors"
	"github.com/fatih/color"
	"github.com/op/go-logging"
	"os"
	"path/filepath"
	"strings"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	m "github.com/shini4i/argo-compare/internal/models"
)

type GitRepo struct {
	CmdRunner    utils.CmdRunner
	changedFiles []string
	invalidFiles []string
}

var (
	gitFileDoesNotExist = errors.New("file does not exist in target branch")
)

func (g *GitRepo) getChangedFiles() ([]string, error) {
	stdout, stderr, err := g.CmdRunner.Run("git", "--no-pager", "diff", "--name-only", targetBranch)
	if err != nil {
		return nil, err
	}

	if logging.GetLevel(loggerName) == logging.DEBUG {
		log.Error(stderr)
	}

	log.Debug("===> Found the following changed files:")
	for _, file := range strings.Split(stdout, "\n") {
		if file != "" {
			log.Debugf("▶ %s", file)
		}
	}

	for _, file := range strings.Split(stdout, "\n") {
		if filepath.Ext(file) == ".yaml" {
			if isApp, err := checkIfApp(file); err != nil {
				if errors.Is(err, m.NotApplicationError) {
					log.Debugf("Skipping non-application file [%s]", file)
					continue
				} else if errors.Is(err, m.UnsupportedAppConfigurationError) {
					log.Warningf("Skipping unsupported application configuration [%s]", file)
					continue
				} else if errors.Is(err, m.EmptyFileError) {
					log.Debugf("Skipping empty file [%s]", file)
					continue
				}
				g.invalidFiles = append(g.invalidFiles, file)
			} else if isApp {
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

	return g.changedFiles, nil
}

func (g *GitRepo) getChangedFileContent(targetBranch string, targetFile string) (m.Application, error) {
	var tmpFile *os.File

	log.Debugf("Getting content of %s from %s", targetFile, targetBranch)

	stdout, stderr, err := g.CmdRunner.Run("git", "--no-pager", "show", targetBranch+":"+targetFile)
	if err != nil {
		if strings.Contains(stderr, "exists on disk, but not in") {
			color.Yellow("The requested file does not exist in target branch, assuming it is a new Application")
		} else {
			return m.Application{}, err
		}

		// unless we want to print the added manifests, we stop here
		if !printAddedManifests {
			return m.Application{}, gitFileDoesNotExist
		}
	}

	// writing the content to a temporary file to be able to pass it to the parser
	if tmpFile, err = os.CreateTemp("/tmp", "compare-*.yaml"); err != nil {
		log.Fatal("Error creating temporary file")
	}

	if _, err = tmpFile.WriteString(stdout); err != nil {
		log.Fatal(err.Error())
	}

	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			log.Fatal(err.Error())
		}
	}(tmpFile.Name())

	target := Target{File: tmpFile.Name()}
	if err := target.parse(); err != nil {
		return m.Application{}, err
	}

	return target.App, nil
}

func checkIfApp(file string) (bool, error) {
	log.Debugf("===> Checking if [%s] is an Application", cyan(file))

	target := Target{File: file}

	if err := target.parse(); err != nil {
		return false, err
	}
	return true, nil
}
