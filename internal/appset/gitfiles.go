package appset

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/shini4i/argo-compare/internal/models"
	"sigs.k8s.io/yaml"
)

// matchFiles selects the files a git generator reads parameters from, sorted so
// the generated order is stable. Patterns are matched with doublestar, as
// ArgoCD does for files, so `**` recurses — unlike a directories pattern.
// Dot directories are not skipped here: ArgoCD filters them for directories
// only, and a tree listing never contains .git in the first place.
func matchFiles(files []string, entries []models.GitFile) []string {
	var matched []string

	for _, file := range files {
		if !matchesAnyFile(file, entries) {
			continue
		}
		matched = append(matched, file)
	}

	sort.Strings(matched)

	return matched
}

// MatchesFile reports whether a git generator reads parameters from file. It
// tests the patterns directly rather than a listing, so a file a change deleted
// still matches.
func MatchesFile(gitGen *models.GitGenerator, file string) bool {
	if gitGen == nil || file == "" {
		return false
	}

	return matchesAnyFile(file, gitGen.Files)
}

// matchesAnyFile reports whether file matches an entry. A malformed pattern
// cannot match, so doublestar's error is not a failure.
func matchesAnyFile(file string, entries []models.GitFile) bool {
	for _, entry := range entries {
		if ok, err := doublestar.Match(entry.Path, file); err == nil && ok {
			return true
		}
	}

	return false
}

// fileParams turns one matched file into the parameter sets it generates; a
// sequence yields one set per element. Content that will not parse is fatal
// rather than skipped, because a merge gate that quietly compares fewer
// Applications is worse than one that fails.
func fileParams(file string, content []byte) ([]map[string]any, error) {
	objects, err := decodeFileObjects(content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}

	params := make([]map[string]any, 0, len(objects))
	for _, object := range objects {
		set := make(map[string]any, len(object)+1)
		for key, value := range object {
			set[key] = value
		}
		set[pathParam] = filePathParams(file)

		params = append(params, set)
	}

	return params, nil
}

// decodeFileObjects reads a file as a single mapping, falling back to a
// sequence of them, matching how ArgoCD accepts both shapes.
func decodeFileObjects(content []byte) ([]map[string]any, error) {
	single := map[string]any{}
	if err := yaml.Unmarshal(content, &single); err == nil {
		return []map[string]any{single}, nil
	}

	var many []map[string]any
	if err := yaml.Unmarshal(content, &many); err != nil {
		return nil, err
	}

	return many, nil
}

// filePathParams describes the directory holding a matched file, plus the file
// name itself. `path` is the directory, not the file, as ArgoCD defines it.
// A file at the repository root therefore reports "." as its path and basename,
// which normalizes away to the empty string — as it does under ArgoCD.
func filePathParams(file string) map[string]any {
	dir := path.Dir(file)
	basename := path.Base(dir)
	filename := path.Base(file)

	return map[string]any{
		pathParam:            dir,
		"segments":           strings.Split(dir, "/"),
		"basename":           basename,
		"basenameNormalized": normalize(basename),
		"filename":           filename,
		"filenameNormalized": normalize(filename),
	}
}
