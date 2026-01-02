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
	"github.com/shini4i/argo-compare/internal/ui"
	"github.com/spf13/afero"
)

// GitRepo wraps interactions with the current repository for diff analysis.
type GitRepo struct {
	repo       *git.Repository
	fs         afero.Fs
	cmdRunner  ports.CmdRunner
	fileReader ports.FileReader
	log        *logging.Logger
}

// ChangedFilesResult encapsulates the changed application files and any invalid manifests.
type ChangedFilesResult struct {
	Applications []string
	Invalid      []string
}

var errGitFileDoesNotExist = errors.New("file does not exist in target branch")

// NewGitRepo opens the Git repository rooted at the current working directory and returns a GitRepo configured with the provided filesystem, command runner, file reader, and logger.
// It locates the repository root and opens the repository; an error is returned if root discovery or repository opening fails.
func NewGitRepo(fs afero.Fs, cmdRunner ports.CmdRunner, fileReader ports.FileReader, log *logging.Logger) (*GitRepo, error) {
	repoRoot, err := GetGitRepoRoot()
	if err != nil {
		return nil, err
	}

	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	return &GitRepo{
		repo:       repo,
		fs:         fs,
		cmdRunner:  cmdRunner,
		fileReader: fileReader,
		log:        log,
	}, nil
}

// GetChangedFiles compares HEAD against targetBranch and returns changed application files.
func (g *GitRepo) GetChangedFiles(targetBranch string, filesToIgnore []string) (ChangedFilesResult, error) {
	targetRef, err := g.repo.Reference(plumbing.ReferenceName(fmt.Sprintf("refs/remotes/origin/%s", targetBranch)), true)
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("failed to resolve target branch %s: %w", targetBranch, err)
	}

	headRef, err := g.repo.Head()
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("failed to get HEAD: %w", err)
	}

	targetCommit, err := g.repo.CommitObject(targetRef.Hash())
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("failed to get commit object for target branch %s: %w", targetBranch, err)
	}

	headCommit, err := g.repo.CommitObject(headRef.Hash())
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("failed to get commit object for current branch: %w", err)
	}

	targetTree, err := targetCommit.Tree()
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("failed to get tree for target commit: %w", err)
	}

	headTree, err := headCommit.Tree()
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("failed to get tree for head commit: %w", err)
	}

	changes, err := object.DiffTree(targetTree, headTree)
	if err != nil {
		return ChangedFilesResult{}, fmt.Errorf("failed to get diff between trees: %w", err)
	}

	foundFiles := make([]string, 0, len(changes))
	removedFiles := make([]string, 0, len(changes))
	for _, change := range changes {
		if change.To.Name == "" {
			removedFiles = append(removedFiles, change.From.Name)
			continue
		}
		foundFiles = append(foundFiles, change.To.Name)
	}

	g.printChangeFile(foundFiles, removedFiles)

	applications, invalid := g.sortChangedFiles(foundFiles)
	filtered := filterIgnored(applications, filesToIgnore)

	return ChangedFilesResult{Applications: filtered, Invalid: invalid}, nil
}

// GetChangedFileContent fetches and parses targetFile from targetBranch.
func (g *GitRepo) GetChangedFileContent(targetBranch, targetFile string, printAdded bool) (models.Application, error) {
	g.log.Debugf("Getting content of %s from %s", targetFile, targetBranch)

	targetTree, err := g.treeForBranch(targetBranch)
	if err != nil {
		return models.Application{}, err
	}

	fileContent, err := g.targetFileContent(targetTree, targetBranch, targetFile, printAdded)
	if err != nil {
		return models.Application{}, err
	}

	return g.parseTargetApplication(fileContent)
}

// treeForBranch resolves the Git tree for the provided remote branch reference.
func (g *GitRepo) treeForBranch(targetBranch string) (*object.Tree, error) {
	targetRef, err := g.repo.Reference(plumbing.ReferenceName("refs/remotes/origin/"+targetBranch), true)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve target branch %s: %w", targetBranch, err)
	}

	targetCommit, err := g.repo.CommitObject(targetRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit object for target branch %s: %w", targetBranch, err)
	}

	targetTree, err := targetCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree for target commit: %w", err)
	}

	return targetTree, nil
}

// targetFileContent retrieves the contents of a manifest from the target branch, respecting print options.
func (g *GitRepo) targetFileContent(targetTree *object.Tree, targetBranch, targetFile string, printAdded bool) (string, error) {
	fileEntry, err := targetTree.File(targetFile)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			g.log.Warning(ui.Yellow(fmt.Sprintf("The requested file %s does not exist in target branch %s, assuming it is a new Application", targetFile, targetBranch)))
			if !printAdded {
				return "", errGitFileDoesNotExist
			}
			return "", nil
		}
		return "", fmt.Errorf("failed to find file %s in target branch %s: %w", targetFile, targetBranch, err)
	}

	if fileEntry == nil {
		return "", nil
	}

	fileContent, err := fileEntry.Contents()
	if err != nil {
		return "", fmt.Errorf("failed to get contents of file %s: %w", targetFile, err)
	}

	return fileContent, nil
}

// parseTargetApplication parses the retrieved manifest content into an Application model.
func (g *GitRepo) parseTargetApplication(fileContent string) (models.Application, error) {
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

// printChangeFile reports the lists of added and removed files at debug level.
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
			g.log.Debugf("▶ %s", ui.Red(file))
		}
	}
}

// sortChangedFiles filters diff results to include only valid Application manifests.
func (g *GitRepo) sortChangedFiles(files []string) (applications []string, invalid []string) {
	for _, file := range files {
		if filepath.Ext(file) != ".yaml" {
			continue
		}

		switch isApp, err := g.checkIfApp(file); {
		case errors.Is(err, models.ErrNotApplication):
			g.log.Debugf("Skipping non-application file [%s]", file)
		case errors.Is(err, models.ErrUnsupportedAppConfiguration):
			g.log.Warningf("Skipping unsupported application configuration [%s]", file)
		case errors.Is(err, models.ErrEmptyFile):
			g.log.Debugf("Skipping empty file [%s]", file)
		case err != nil:
			g.log.Errorf("Error checking if [%s] is an Application: %s", file, err)
			invalid = append(invalid, file)
		case isApp:
			applications = append(applications, file)
		}
	}

	if len(applications) > 0 {
		g.log.Info("===> Found the following changed Application files")
		for _, file := range applications {
			g.log.Infof("▶ %s", ui.Yellow(file))
		}
	}

	return applications, invalid
}

// checkIfApp determines whether the provided path points to a valid Application manifest.
func (g *GitRepo) checkIfApp(file string) (bool, error) {
	g.log.Debugf("===> Checking if [%s] is an Application", ui.Cyan(file))

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

// GetGitRepoRoot returns the filesystem path of the nearest parent directory,
// starting from the current working directory, that contains a Git repository.
// It returns an error if the current working directory cannot be determined or
// if no Git repository is found in any ancestor directories.
func GetGitRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
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

// filterIgnored filters out files that appear in the ignored list.
// If the ignored list is empty, the input slice is returned unchanged.
// Comparison is by exact string match and the order of remaining files is preserved.
func filterIgnored(files, ignored []string) []string {
	if len(ignored) == 0 {
		return files
	}

	ignoredSet := make(map[string]struct{}, len(ignored))
	for _, file := range ignored {
		ignoredSet[file] = struct{}{}
	}

	filtered := make([]string, 0, len(files))
	for _, file := range files {
		if _, ok := ignoredSet[file]; ok {
			continue
		}
		filtered = append(filtered, file)
	}

	return filtered
}
