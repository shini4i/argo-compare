package app

import (
	"fmt"
	"path"
	"sort"

	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/shini4i/argo-compare/internal/models"
)

// assertGitGeneratorsComparable rejects a git generator whose directories the
// two branch legs cannot stand in for: one reading another repository, or one
// pinned to a revision that is neither HEAD nor the branch being compared.
func assertGitGeneratorsComparable(appSet *models.ApplicationSet, originURL, targetBranch string) error {
	for _, generator := range appSet.Spec.Generators {
		if generator.Git == nil {
			continue
		}

		if !repoIdentityMatches(generator.Git.RepoURL, originURL) {
			return fmt.Errorf("%w: git generator repoURL %q is not this repository (%q); only same-repository generators are supported",
				models.ErrUnsupportedAppConfiguration, redactRepo(generator.Git.RepoURL), redactRepo(originURL))
		}

		// A pinned revision keeps ArgoCD generating from a fixed tree, so a
		// directory this branch adds would change nothing it deploys.
		if rev := generator.Git.Revision; rev != "" && rev != "HEAD" && rev != targetBranch {
			return fmt.Errorf("%w: git generator revision %q is neither HEAD nor the compared branch %q, so the branches cannot stand in for it",
				models.ErrUnsupportedAppConfiguration, rev, targetBranch)
		}
	}

	return nil
}

// gitTree reads one comparison leg out of a Git tree. Listing from the tree
// rather than the filesystem keeps both legs alike: the working tree also holds
// untracked and ignored files, which Git does not record and ArgoCD never sees.
type gitTree struct {
	tree *object.Tree
}

// Directories reports every directory the tree records, derived from its file
// paths because Git stores no empty directories.
func (g gitTree) Directories() ([]string, error) {
	files, err := g.Files()
	if err != nil {
		return nil, err
	}

	return directoriesOf(files), nil
}

// Files reports every file path the tree records.
func (g gitTree) Files() ([]string, error) {
	var files []string

	if err := g.tree.Files().ForEach(func(file *object.File) error {
		// A symlink's blob holds its target path, not the target's content, so
		// reading one as generator input would parse the link text as YAML.
		if file.Mode != filemode.Regular && file.Mode != filemode.Executable {
			return nil
		}
		files = append(files, file.Name)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("list tree contents: %w", err)
	}

	sort.Strings(files)

	return files, nil
}

// ReadFile returns the contents of one file recorded in the tree.
func (g gitTree) ReadFile(path string) ([]byte, error) {
	entry, err := g.tree.File(path)
	if err != nil {
		return nil, fmt.Errorf("find %s in tree: %w", path, err)
	}

	// A generator pattern is written by the pull request, so it can match a
	// blob of any size; the same bound the manifest scan uses applies here.
	if entry.Size > maxManifestBytes {
		return nil, fmt.Errorf("%s is %d bytes, over the %d byte limit for generator input", path, entry.Size, maxManifestBytes)
	}

	content, err := entry.Contents()
	if err != nil {
		return nil, fmt.Errorf("read %s from tree: %w", path, err)
	}

	return []byte(content), nil
}

// directoriesOf derives the directory set implied by a list of file paths.
// Deriving from files rather than walking directories keeps both legs alike:
// Git stores no empty directories, so one could never generate an Application.
func directoriesOf(files []string) []string {
	seen := make(map[string]bool, len(files))

	for _, file := range files {
		for dir := path.Dir(file); dir != "." && dir != "/" && !seen[dir]; dir = path.Dir(dir) {
			seen[dir] = true
		}
	}

	dirs := make([]string, 0, len(seen))
	for dir := range seen {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	return dirs
}
