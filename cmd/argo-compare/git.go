package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	interfaces "github.com/shini4i/argo-compare/cmd/argo-compare/interfaces"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/spf13/afero"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/models"
)

type GitRepo struct {
	Repo         *git.Repository
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
	// Retrieve the target branch reference.
	targetBranch := fmt.Sprintf("refs/heads/%s", "main")
	targetRef, err := g.Repo.Reference(plumbing.ReferenceName(targetBranch), true)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve target branch %s: %v", targetBranch, err)
	}

	// Retrieve the current branch reference.
	headRef, err := g.Repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %v", err)
	}

	// Get the commit objects for the target branch and the current branch.
	targetCommit, err := g.Repo.CommitObject(targetRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit object for target branch %s: %v", targetBranch, err)
	}

	headCommit, err := g.Repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit object for current branch: %v", err)
	}

	// Get the tree objects for the target commit and the current commit.
	targetTree, err := targetCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree for target commit: %v", err)
	}

	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree for head commit: %v", err)
	}

	// Get the diff between the two trees.
	changes, err := object.DiffTree(targetTree, headTree)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff between trees: %v", err)
	}

	// Collect all the changed files
	var foundFiles []string
	for _, change := range changes {
		foundFiles = append(foundFiles, change.To.Name)
	}

	printChangeFile(foundFiles)
	g.sortChangedFiles(fileReader, foundFiles)

	return foundFiles, nil
}

func (g *GitRepo) getChangedFileContent(targetBranch string, targetFile string) (models.Application, error) {
	log.Debugf("Getting content of %s from %s", targetFile, targetBranch)

	// Retrieve the target branch reference.
	targetRef, err := g.Repo.Reference(plumbing.ReferenceName("refs/heads/"+targetBranch), true)
	if err != nil {
		return models.Application{}, fmt.Errorf("failed to resolve target branch %s: %v", targetBranch, err)
	}

	// Get the commit object for the target branch.
	targetCommit, err := g.Repo.CommitObject(targetRef.Hash())
	if err != nil {
		return models.Application{}, fmt.Errorf("failed to get commit object for target branch %s: %v", targetBranch, err)
	}

	// Get the tree object for the target commit.
	targetTree, err := targetCommit.Tree()
	if err != nil {
		return models.Application{}, fmt.Errorf("failed to get tree for target commit: %v", err)
	}

	// Find the file entry in the tree.
	fileEntry, err := targetTree.File(targetFile)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			log.Warningf("The requested file %s does not exist in target branch %s, assuming it is a new Application", targetFile, targetBranch)
			return models.Application{}, gitFileDoesNotExist
		}
		return models.Application{}, fmt.Errorf("failed to find file %s in target branch %s: %v", targetFile, targetBranch, err)
	}

	// Get the file content.
	fileContent, err := fileEntry.Contents()
	if err != nil {
		return models.Application{}, fmt.Errorf("failed to get contents of file %s: %v", targetFile, err)
	}

	// Create a temporary file with the content.
	tmpFile, err := helpers.CreateTempFile(g.FsType, fileContent)
	if err != nil {
		return models.Application{}, err
	}

	defer func(file afero.File) {
		if err := afero.Fs.Remove(g.FsType, file.Name()); err != nil {
			log.Errorf("Failed to remove temporary file [%s]: %s", file.Name(), err)
		}
	}(tmpFile)

	// Create a Target object and parse the application.
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

// ReadFile reads the contents of the specified file and returns them as a byte slice.
// If the file does not exist, it prints a message indicating that the file was removed in a source branch and returns nil.
// The function handles the os.ErrNotExist error to detect if the file is missing.
func ReadFile(file string) []byte {
	if readFile, err := os.ReadFile(file); errors.Is(err, os.ErrNotExist) /* #nosec G304 */ {
		return nil
	} else {
		return readFile
	}
}

func NewGitRepo(fs afero.Fs, cmdRunner interfaces.CmdRunner) (*GitRepo, error) {
	repoRoot, err := GetGitRepoRoot()
	if err != nil {
		return nil, err
	}

	gitRepo := &GitRepo{
		FsType:    fs,
		CmdRunner: cmdRunner,
	}

	gitRepo.Repo, err = git.PlainOpen(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %v", err)
	}

	return gitRepo, nil
}
