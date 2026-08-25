package app

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shini4i/argo-compare/internal/appset"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/spf13/afero"
)

// appSetKindMarker is the cheap pre-filter that keeps the repository scan from
// parsing every YAML file it finds.
var appSetKindMarker = []byte("ApplicationSet")

// maxManifestBytes bounds what the scan reads. An ApplicationSet manifest is a
// few kilobytes; anything larger is generated output, not one to expand.
const maxManifestBytes = 1 << 20

// DiscoverApplicationSets returns the manifests whose git generators cover a
// directory the diff touched. A git generator's Applications change when
// directories appear or disappear, which leaves the manifest itself untouched,
// so those changes would otherwise never reach the ApplicationSet flow.
func DiscoverApplicationSets(filesystem afero.Fs, fileReader ports.FileReader, repoRoot, originURL, targetBranch string, changedFiles []string) ([]string, []string, error) {
	// A file at the repository root touches no directory but can still match a
	// file generator's pattern, so both lists gate the scan.
	touched := touchedDirectories(changedFiles)
	changed := slashPaths(changedFiles)
	if len(changed) == 0 {
		return nil, nil, nil
	}

	candidates, unusable, err := findApplicationSets(filesystem, fileReader, repoRoot)
	if err != nil {
		return nil, nil, err
	}

	var matched []string
	for _, candidate := range candidates {
		// A generator naming another repository cannot be expanded here, so
		// enqueueing it would only warn about a manifest nobody touched.
		if assertGitGeneratorsComparable(candidate.appSet, originURL, targetBranch) != nil {
			continue
		}
		if covers(candidate.appSet, touched, changed) {
			matched = append(matched, candidate.path)
		}
	}

	sort.Strings(matched)
	sort.Strings(unusable)

	return matched, unusable, nil
}

// discoveredAppSet pairs a parsed manifest with its repo-relative path.
type discoveredAppSet struct {
	path   string
	appSet *models.ApplicationSet
}

// covers reports whether a git generator in appSet reads something the diff
// touched: a directory it generates for, or a file it takes parameters from.
func covers(appSet *models.ApplicationSet, dirs, files []string) bool {
	for _, generator := range appSet.Spec.Generators {
		if generator.Git == nil {
			continue
		}

		for _, dir := range dirs {
			if appset.Matches(generator.Git, dir) {
				return true
			}
		}

		for _, file := range files {
			if appset.MatchesFile(generator.Git, file) {
				return true
			}
		}
	}

	return false
}

// slashPaths normalises changed paths to the separator patterns are written in.
func slashPaths(paths []string) []string {
	normalised := make([]string, 0, len(paths))
	for _, p := range paths {
		if p != "" {
			normalised = append(normalised, filepath.ToSlash(p))
		}
	}

	return normalised
}

// touchedDirectories returns every ancestor directory of the changed files,
// because a generator may match any level of the path, not only the deepest.
func touchedDirectories(changedFiles []string) []string {
	seen := make(map[string]bool, len(changedFiles))

	for _, file := range changedFiles {
		if file == "" {
			continue
		}
		for dir := path.Dir(filepath.ToSlash(file)); dir != "." && dir != "/" && !seen[dir]; dir = path.Dir(dir) {
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

// findApplicationSets walks the working tree for expandable ApplicationSet
// manifests. It also returns the files that name the kind but could not be read
// or parsed, so the caller can report them instead of losing them silently.
func findApplicationSets(filesystem afero.Fs, fileReader ports.FileReader, repoRoot string) (found []discoveredAppSet, unusable []string, err error) {
	walkErr := afero.Walk(filesystem, repoRoot, func(fullPath string, info fs.FileInfo, err error) error {
		// An unreadable entry is skipped, not fatal: this scan visits the whole
		// repository, and one directory it cannot enter is not this run.
		if err != nil {
			return skipUnreadable(info)
		}

		rel, relErr := filepath.Rel(repoRoot, fullPath)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		if info.IsDir() {
			return skipHiddenDir(rel, info)
		}

		if info.Size() > maxManifestBytes {
			return nil
		}

		appSet, readErr := readApplicationSet(fileReader, fullPath, rel)
		switch {
		case readErr != nil:
			unusable = append(unusable, fmt.Sprintf("%s (%s)", rel, readErr))
		case appSet != nil:
			found = append(found, discoveredAppSet{path: rel, appSet: appSet})
		}

		return nil
	})
	if walkErr != nil {
		return nil, nil, fmt.Errorf("scan for ApplicationSet manifests: %w", walkErr)
	}

	return found, unusable, nil
}

// discoverGeneratorApplicationSets finds ApplicationSets a directory change
// affects and reports them, so a run that touches no manifest still compares
// the Applications a git generator adds or drops.
func (g *GitRepo) discoverGeneratorApplicationSets(repoRoot, targetBranch string, found, removed, filesToIgnore []string) ([]string, error) {
	changed := filterIgnored(append(append([]string{}, found...), removed...), filesToIgnore)

	originURL, err := g.OriginURL()
	if err != nil {
		return nil, err
	}

	matched, unusable, err := DiscoverApplicationSets(g.fs, g.fileReader, repoRoot, originURL, targetBranch, changed)
	if err != nil {
		return nil, err
	}

	// A manifest naming the kind but failing to load may well be one this
	// change affects, so say so rather than dropping it without a word.
	for _, file := range unusable {
		g.log.Warningf("Skipping unreadable ApplicationSet manifest during discovery: %s", file)
	}

	matched = filterIgnored(matched, filesToIgnore)
	for _, file := range matched {
		g.log.Debugf("Directory change affects ApplicationSet [%s]", file)
	}

	return matched, nil
}

// mergePaths appends the paths of extra that base does not already carry,
// preserving base's order so the reported list stays stable.
func mergePaths(base, extra []string) []string {
	seen := make(map[string]bool, len(base))
	for _, file := range base {
		seen[file] = true
	}

	for _, file := range extra {
		if !seen[file] {
			base = append(base, file)
			seen[file] = true
		}
	}

	return base
}

// readApplicationSet returns the expandable ApplicationSet at fullPath, nil when
// the file is not one, or an error when a file that looks like one cannot be
// used. The caller reports that rather than failing, so a single broken
// manifest does not stop a run it may have nothing to do with.
func readApplicationSet(fileReader ports.FileReader, fullPath, rel string) (*models.ApplicationSet, error) {
	if ext := path.Ext(rel); ext != ".yaml" && ext != ".yml" {
		return nil, nil
	}

	content, err := fileReader.ReadFile(fullPath)
	if err != nil {
		return nil, err
	}

	// Only a file naming the kind is worth reporting on; every other YAML in
	// the repository is simply not a manifest this scan wants.
	if !bytes.Contains(content, appSetKindMarker) {
		return nil, nil
	}

	appSet, err := parseApplicationSetContent(content)
	if err != nil {
		return nil, err
	}

	return appSet, nil
}

// skipUnreadable abandons an entry the walk could not stat, descending no
// further when it is a directory.
func skipUnreadable(info fs.FileInfo) error {
	if info != nil && info.IsDir() {
		return filepath.SkipDir
	}

	return nil
}

// skipHiddenDir prunes dot directories, which hold no manifest worth expanding
// and would otherwise make the scan walk the whole .git object store.
func skipHiddenDir(rel string, info fs.FileInfo) error {
	if rel != "." && strings.HasPrefix(info.Name(), ".") {
		return filepath.SkipDir
	}

	return nil
}
