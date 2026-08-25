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
func DiscoverApplicationSets(filesystem afero.Fs, fileReader ports.FileReader, repoRoot, originURL, targetBranch string, changedFiles []string) ([]string, error) {
	touched := touchedDirectories(changedFiles)
	if len(touched) == 0 {
		return nil, nil
	}

	candidates, err := findApplicationSets(filesystem, fileReader, repoRoot)
	if err != nil {
		return nil, err
	}

	var matched []string
	for _, candidate := range candidates {
		// A generator naming another repository cannot be expanded here, so
		// enqueueing it would only warn about a manifest nobody touched.
		if assertGitGeneratorsComparable(candidate.appSet, originURL, targetBranch) != nil {
			continue
		}
		if coversAnyDirectory(candidate.appSet, touched) {
			matched = append(matched, candidate.path)
		}
	}

	sort.Strings(matched)

	return matched, nil
}

// discoveredAppSet pairs a parsed manifest with its repo-relative path.
type discoveredAppSet struct {
	path   string
	appSet *models.ApplicationSet
}

// coversAnyDirectory reports whether a git generator in appSet would generate
// an Application for one of the supplied directories.
func coversAnyDirectory(appSet *models.ApplicationSet, dirs []string) bool {
	for _, generator := range appSet.Spec.Generators {
		if generator.Git == nil {
			continue
		}
		for _, dir := range dirs {
			if appset.Matches(generator.Git, dir) {
				return true
			}
		}
	}

	return false
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
// manifests that declare a git generator. Manifests that fail to parse or are
// unsupported are skipped: a changed one is reported by discovery already, and
// an unchanged one is not this run's concern.
func findApplicationSets(filesystem afero.Fs, fileReader ports.FileReader, repoRoot string) ([]discoveredAppSet, error) {
	var found []discoveredAppSet

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

		if appSet := readApplicationSet(fileReader, fullPath, rel); appSet != nil {
			found = append(found, discoveredAppSet{path: rel, appSet: appSet})
		}

		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("scan for ApplicationSet manifests: %w", walkErr)
	}

	return found, nil
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

	matched, err := DiscoverApplicationSets(g.fs, g.fileReader, repoRoot, originURL, targetBranch, changed)
	if err != nil {
		return nil, err
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

// readApplicationSet returns the expandable ApplicationSet at fullPath, or nil
// when the file is not one. A read or parse failure yields nil rather than an
// error: this scan visits every YAML in the repository, and one it cannot use
// is not this run's concern.
func readApplicationSet(fileReader ports.FileReader, fullPath, rel string) *models.ApplicationSet {
	if ext := path.Ext(rel); ext != ".yaml" && ext != ".yml" {
		return nil
	}

	content, err := fileReader.ReadFile(fullPath)
	if err != nil || !bytes.Contains(content, appSetKindMarker) {
		return nil
	}

	appSet, err := parseApplicationSetContent(content)
	if err != nil {
		return nil
	}

	return appSet
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
