package appset

import (
	"path"
	"sort"
	"strings"

	"github.com/shini4i/argo-compare/internal/models"
)

// Tree gives expansion read access to the branch being compared. Paths are
// relative to the repository root and separated by "/". It is consulted only
// when the ApplicationSet declares a git generator.
type Tree interface {
	Directories() ([]string, error)
	Files() ([]string, error)
	ReadFile(path string) ([]byte, error)
}

// matchDirectories selects the directories a git generator generates an
// Application for, sorted so the generated order is stable. A directory is
// selected when it matches an including entry and no excluding one; ArgoCD
// gives exclude the higher priority regardless of the order entries appear in.
func matchDirectories(dirs []string, entries []models.GitDirectory) []string {
	var matched []string

	for _, dir := range dirs {
		if hasDotSegment(dir) || !matchesAny(dir, entries, false) || matchesAny(dir, entries, true) {
			continue
		}
		matched = append(matched, dir)
	}

	sort.Strings(matched)

	return matched
}

// Matches reports whether a git generator would generate an Application for
// dir. It tests the patterns directly rather than a directory listing, so a
// directory a change deleted still matches.
func Matches(gitGen *models.GitGenerator, dir string) bool {
	// path.Match("*", "") reports a match, so the empty string is rejected
	// outright rather than standing in for the repository root.
	if gitGen == nil || dir == "" || hasDotSegment(dir) {
		return false
	}

	return matchesAny(dir, gitGen.Directories, false) && !matchesAny(dir, gitGen.Directories, true)
}

// matchesAny reports whether dir matches an entry whose Exclude equals exclude.
// A malformed pattern cannot match, so path.Match's error is not a failure.
func matchesAny(dir string, entries []models.GitDirectory, exclude bool) bool {
	for _, entry := range entries {
		if entry.Exclude != exclude {
			continue
		}
		if ok, err := path.Match(entry.Path, dir); err == nil && ok {
			return true
		}
	}

	return false
}

// hasDotSegment reports whether any segment of dir starts with a dot, which is
// how ArgoCD keeps .git and its neighbours from generating Applications.
func hasDotSegment(dir string) bool {
	for _, segment := range strings.Split(dir, "/") {
		if strings.HasPrefix(segment, ".") {
			return true
		}
	}

	return false
}

// directoryParams builds the parameter set one matched directory renders with,
// nested so goTemplate reaches it as .path.path, .path.basename and the rest.
func directoryParams(dir string) map[string]any {
	basename := path.Base(dir)

	return map[string]any{
		pathParam: map[string]any{
			pathParam:            dir,
			"segments":           strings.Split(dir, "/"),
			"basename":           basename,
			"basenameNormalized": normalize(basename),
		},
	}
}
