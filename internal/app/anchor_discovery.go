package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shini4i/argo-compare/internal/anchor"

	"github.com/spf13/afero"
)

// AnchorGroup is the result of grouping changed files by the nearest
// enclosing `.argo-compare.yml`. Dir is the absolute path of the directory
// that contains the anchor file. ChangedFiles is the subset of the
// caller-supplied diff that lives at or below Dir, preserved with the
// caller's original relative paths.
type AnchorGroup struct {
	Dir          string
	Anchor       anchor.Anchor
	ChangedFiles []string
}

// DiscoverAnchors walks up from each changed file to find the nearest
// directory inside repoRoot that contains a file named anchorFileName,
// loads that anchor once per directory, and groups the changed files by
// their anchor directory.
//
// Files with no enclosing anchor inside repoRoot are silently dropped:
// they belong to the existing changed-Application flow, not the anchor
// flow. Changes to the anchor file itself are skipped: it is a marker,
// not application content, so a marker-only change seeds no group. A
// malformed anchor file is a hard error so the caller can fail loudly;
// partial results would silently hide a broken configuration.
//
// The returned slice is sorted by Dir for deterministic downstream logs
// and comments.
func DiscoverAnchors(repoRoot string, changedFiles []string, fs afero.Fs, anchorFileName string) ([]AnchorGroup, error) {
	repoRoot = filepath.Clean(repoRoot)

	cache := map[string]anchor.Anchor{}
	files := map[string][]string{}

	for _, rel := range changedFiles {
		// The anchor file is a marker, not application content. A change to the
		// marker itself must never seed or extend a group: onboarding hundreds
		// of `.argo-compare.yml` files would otherwise trigger comparisons with
		// no real changes. Real content alongside the marker still forms a group.
		if filepath.Base(rel) == filepath.Base(anchorFileName) {
			continue
		}
		dir, err := findAnchorDir(fs, repoRoot, filepath.Dir(filepath.Join(repoRoot, rel)), anchorFileName)
		if err != nil {
			return nil, err
		}
		if dir == "" {
			continue
		}
		if _, ok := cache[dir]; !ok {
			a, err := anchor.Load(fs, filepath.Join(dir, anchorFileName))
			if err != nil {
				return nil, err
			}
			cache[dir] = a
		}
		files[dir] = append(files[dir], rel)
	}

	groups := make([]AnchorGroup, 0, len(cache))
	for dir, a := range cache {
		dirFiles := files[dir]
		sort.Strings(dirFiles)
		groups = append(groups, AnchorGroup{
			Dir:          dir,
			Anchor:       a,
			ChangedFiles: dirFiles,
		})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Dir < groups[j].Dir })
	return groups, nil
}

// findAnchorDir walks from start upward until it finds a directory containing
// anchorFileName, returning that directory's absolute path. It stops at
// (and includes) repoRoot, and returns "" if no anchor exists in the chain.
func findAnchorDir(fs afero.Fs, repoRoot, start, anchorFileName string) (string, error) {
	dir := start
	for {
		if !pathWithin(repoRoot, dir) {
			return "", nil
		}
		exists, err := afero.Exists(fs, filepath.Join(dir, anchorFileName))
		if err != nil {
			return "", fmt.Errorf("check %s at %q: %w", anchorFileName, dir, err)
		}
		if exists {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// pathWithin reports whether dir is repoRoot itself or a descendant of it.
func pathWithin(repoRoot, dir string) bool {
	if dir == repoRoot {
		return true
	}
	rel, err := filepath.Rel(repoRoot, dir)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
