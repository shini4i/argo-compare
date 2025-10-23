package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/spf13/afero"
)

type GitRepo struct {
	repo         *git.Repository
	fs           afero.Fs
	cmdRunner    ports.CmdRunner
	fileReader   ports.FileReader
	log          *logging.Logger
	changedFiles []string
	invalidFiles []string
}

var gitFileDoesNotExist = errors.New("file does not exist in target branch")

func NewGitRepo(fs afero.Fs, cmdRunner ports.CmdRunner, fileReader ports.FileReader, log *logging.Logger) (*GitRepo, error) {
	repoRoot, err := GetGitRepoRoot()
	if err != nil {
		return nil, err
	}

	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %v", err)
	}

	return &GitRepo{
		repo:       repo,
		fs:         fs,
		cmdRunner:  cmdRunner,
		fileReader: fileReader,
		log:        log,
	}, nil
}

func (g *GitRepo) GetChangedFiles(targetBranch string, filesToIgnore []string) ([]string, error) {
	targetRef, err := g.repo.Reference(plumbing.ReferenceName(fmt.Sprintf("refs/remotes/origin/%s", targetBranch)), true)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve target branch %s: %v", targetBranch, err)
	}

	headRef, err := g.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %v", err)
	}

	targetCommit, err := g.repo.CommitObject(targetRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit object for target branch %s: %v", targetBranch, err)
	}

	headCommit, err := g.repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit object for current branch: %v", err)
	}

	targetTree, err := targetCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree for target commit: %v", err)
	}

	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree for head commit: %v", err)
	}

	changes, err := object.DiffTree(targetTree, headTree)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff between trees: %v", err)
	}

	var foundFiles, removedFiles []string
	for _, change := range changes {
		if change.To.Name == "" {
			removedFiles = append(removedFiles, change.From.Name)
			continue
		}
		foundFiles = append(foundFiles, change.To.Name)
	}

	g.printChangeFile(foundFiles, removedFiles)
	g.sortChangedFiles(foundFiles)

	filtered := filterIgnored(g.changedFiles, filesToIgnore)

	return filtered, nil
}

func (g *GitRepo) GetChangedFileContent(targetBranch, targetFile string, printAdded bool) (models.Application, error) {
	g.log.Debugf("Getting content of %s from %s", targetFile, targetBranch)

	targetRef, err := g.repo.Reference(plumbing.ReferenceName("refs/remotes/origin/"+targetBranch), true)
	if err != nil {
		return models.Application{}, fmt.Errorf("failed to resolve target branch %s: %v", targetBranch, err)
	}

	targetCommit, err := g.repo.CommitObject(targetRef.Hash())
	if err != nil {
		return models.Application{}, fmt.Errorf("failed to get commit object for target branch %s: %v", targetBranch, err)
	}

	targetTree, err := targetCommit.Tree()
	if err != nil {
		return models.Application{}, fmt.Errorf("failed to get tree for target commit: %v", err)
	}

	fileEntry, err := targetTree.File(targetFile)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			g.log.Warningf("\u001B[33mThe requested file %s does not exist in target branch %s, assuming it is a new Application\u001B[0m", targetFile, targetBranch)
			if !printAdded {
				return models.Application{}, gitFileDoesNotExist
			}
		} else {
			return models.Application{}, fmt.Errorf("failed to find file %s in target branch %s: %v", targetFile, targetBranch, err)
		}
	}

	var fileContent string
	if fileEntry == nil {
		fileContent = ""
	} else {
		fileContent, err = fileEntry.Contents()
		if err != nil {
			return models.Application{}, fmt.Errorf("failed to get contents of file %s: %v", targetFile, err)
		}
	}

	tmpFile, err := helpers.CreateTempFile(g.fs, fileContent)
	if err != nil {
		return models.Application{}, err
	}

	defer func(file afero.File) {
		if err := afero.Fs.Remove(g.fs, file.Name()); err != nil {
			g.log.Errorf("Failed to remove temporary file [%s]: %s", file.Name(), err)
		}
	}(tmpFile)

	target := Target{
		CmdRunner:  g.cmdRunner,
		FileReader: g.fileReader,
		Log:        g.log,
		File:       tmpFile.Name(),
	}
	if err := target.parse(); err != nil {
		return models.Application{}, fmt.Errorf("failed to parse the application: %w", err)
	}

	return target.App, nil
}

func (g *GitRepo) PrintInvalidFiles() error {
	if len(g.invalidFiles) > 0 {
		g.log.Info("===> The following yaml files are invalid and were skipped")
		for _, file := range g.invalidFiles {
			g.log.Warningf("▶ %s", file)
		}
		return errors.New("invalid files found")
	}
	return nil
}

func (g *GitRepo) printChangeFile(addedFiles, removed []string) {
	g.log.Debug("===> Found the following changed files:")
	for _, file := range addedFiles {
		if file != "" {
			g.log.Debugf("▶ %s", file)
		}
	}
	g.log.Debug("===> Found the following removed files:")
	for _, file := range removed {
		if file != "" {
			g.log.Debugf("▶ %s", red(file))
		}
	}
}

func (g *GitRepo) sortChangedFiles(files []string) {
	for _, file := range files {
		if filepath.Ext(file) != ".yaml" {
			continue
		}

		switch isApp, err := g.checkIfApp(file); {
		case errors.Is(err, models.NotApplicationError):
			g.log.Debugf("Skipping non-application file [%s]", file)
		case errors.Is(err, models.UnsupportedAppConfigurationError):
			g.log.Warningf("Skipping unsupported application configuration [%s]", file)
		case errors.Is(err, models.EmptyFileError):
			g.log.Debugf("Skipping empty file [%s]", file)
		case err != nil:
			g.log.Errorf("Error checking if [%s] is an Application: %s", file, err)
			g.invalidFiles = append(g.invalidFiles, file)
		case isApp:
			g.log.Infof("▶ %s", yellow(file))
			g.changedFiles = append(g.changedFiles, file)
		}
	}
}

func (g *GitRepo) checkIfApp(file string) (bool, error) {
	g.log.Debugf("===> Checking if [%s] is an Application", cyan(file))

	target := Target{
		CmdRunner:  g.cmdRunner,
		FileReader: g.fileReader,
		Log:        g.log,
		File:       file,
	}

	if err := target.parse(); err != nil {
		return false, err
	}
	return true, nil
}

func GetGitRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %v", err)
	}

	for {
		_, err := git.PlainOpen(dir)
		if err == nil {
			return dir, nil
		}

		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			break
		}

		dir = parentDir
	}

	return "", fmt.Errorf("no git repository found")
}

func readFile(file string) []byte {
	data, err := os.ReadFile(file) // #nosec G304
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return nil
	}
	return data
}

func filterIgnored(files []string, ignored []string) []string {
	if len(ignored) == 0 {
		return files
	}

	ignoredSet := make(map[string]struct{}, len(ignored))
	for _, file := range ignored {
		ignoredSet[file] = struct{}{}
	}

	var filtered []string
	for _, file := range files {
		if _, ok := ignoredSet[file]; ok {
			continue
		}
		filtered = append(filtered, file)
	}

	return filtered
}
