package app

import (
	"fmt"
	"path"
	"sort"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/shini4i/argo-compare/internal/appset"
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
				models.ErrUnsupportedAppConfiguration, generator.Git.RepoURL, originURL)
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

// treeLister lists the directories recorded in a Git tree.
func treeLister(tree *object.Tree) appset.DirectoryLister {
	return func() ([]string, error) {
		var files []string

		if err := tree.Files().ForEach(func(file *object.File) error {
			files = append(files, file.Name)
			return nil
		}); err != nil {
			return nil, fmt.Errorf("list target branch directories: %w", err)
		}

		return directoriesOf(files), nil
	}
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
