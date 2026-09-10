package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ui"
)

// ErrUnknownValueFileRef is returned when a helm.valueFiles entry uses a
// "$name/" prefix that no sibling source declares as its ref.
var ErrUnknownValueFileRef = errors.New("valueFiles entry references an undeclared ref source")

// ErrInvalidValueFilePath is returned for a helm.valueFiles entry that could
// read outside the directory it is resolved against.
var ErrInvalidValueFilePath = errors.New("invalid valueFiles path")

// refFile identifies one file inside a ref source's repository.
type refFile struct {
	Ref  string
	Path string
}

// splitRefValueFile splits an ArgoCD "$ref/path" valueFiles entry into the ref
// name and the path inside that ref source. ok is false for a plain entry,
// which resolves against the chart directory instead.
func splitRefValueFile(entry string) (refName, rel string, ok bool) {
	if !strings.HasPrefix(entry, "$") {
		return "", "", false
	}
	name, rest, found := strings.Cut(strings.TrimPrefix(entry, "$"), "/")
	if !found || name == "" {
		return "", "", false
	}
	return name, rest, true
}

// refSources maps each declared ref name to the source declaring it. A name
// becomes a directory under the render workspace, and it comes from the
// Application, so a name that is not a single plain path segment is rejected
// here rather than relied on to be harmless once joined.
func (t *Target) refSources() (map[string]*models.Source, error) {
	refs := make(map[string]*models.Source)
	for _, source := range t.App.Spec.Sources {
		if !source.IsRef() {
			continue
		}
		if err := validateRefName(source.Ref); err != nil {
			return nil, err
		}
		// A same-repo and a remote ref are read from different places, so
		// letting the last declaration win would resolve against the wrong one.
		if _, dup := refs[source.Ref]; dup {
			return nil, fmt.Errorf("%w: ref name %q is declared by more than one source", ErrInvalidValueFilePath, source.Ref)
		}
		refs[source.Ref] = source
	}
	return refs, nil
}

// validateRefName requires a ref name to be one plain path segment.
func validateRefName(name string) error {
	if name == "." || name == ".." || strings.ContainsRune(name, filepath.Separator) || strings.Contains(name, "/") {
		return fmt.Errorf("%w: ref name %q must be a single path segment", ErrInvalidValueFilePath, name)
	}
	return nil
}

// refsThisRepo reports whether a used ref source points at originURL, which
// makes an Application renderable from a registry chart still sensitive to a
// change in this repository — its values files live here.
func refsThisRepo(target *Target, originURL string) (bool, error) {
	used, err := target.usedRefFiles()
	if err != nil {
		return false, err
	}
	if len(used) == 0 {
		return false, nil
	}
	refs, err := target.refSources()
	if err != nil {
		return false, err
	}
	for _, rf := range used {
		if repoIdentityMatches(refs[rf.Ref].RepoURL, originURL) {
			return true, nil
		}
	}
	return false, nil
}

// refDir is the directory a ref source's files are materialized into for this
// comparison leg. It sits under TmpDir so the renderer can assert that every
// resolved values file stays inside the run's temporary directory.
func (t *Target) refDir(refName string) string {
	return filepath.Join(t.TmpDir, "refs", t.Type, refName)
}

// chartDirFor is the directory a renderable source's chart is materialized
// into, matching the layout produced by chart extraction.
func (t *Target) chartDirFor(source *models.Source) string {
	return filepath.Join(t.TmpDir, "charts", t.Type, effectiveChartName(source))
}

// resolveValueFiles turns a source's helm.valueFiles into absolute paths,
// preserving order because Helm lets a later file override an earlier one.
// Plain entries resolve against the chart directory; "$ref/path" entries
// resolve against the materialized ref source.
func (t *Target) resolveValueFiles(source *models.Source) ([]string, error) {
	if source == nil {
		return nil, nil
	}
	refs, err := t.refSources()
	if err != nil {
		return nil, err
	}
	resolved := make([]string, 0, len(source.Helm.ValueFiles))
	for _, entry := range source.Helm.ValueFiles {
		rf, isRef, err := t.refFileFor(entry, refs)
		if err != nil {
			return nil, err
		}
		resolvedPath := filepath.Join(t.chartDirFor(source), entry)
		if isRef {
			resolvedPath = filepath.Join(t.refDir(rf.Ref), rf.Path)
		}
		// One check covers both: a ref file the leg could not materialize is
		// as absent on disk as a chart-relative file that does not exist.
		if t.skipMissingValueFile(source, entry, resolvedPath) {
			continue
		}
		resolved = append(resolved, resolvedPath)
	}
	return resolved, nil
}

// skipMissingValueFile reports whether an absent values file should be dropped
// under helm.ignoreMissingValueFiles. helm fails on a --values path that does
// not exist, so the entry has to go before the argv is built. Any other stat
// error is left for helm to report.
func (t *Target) skipMissingValueFile(source *models.Source, entry, resolved string) bool {
	if !source.Helm.IgnoreMissingValueFiles {
		return false
	}
	if _, err := os.Stat(resolved); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	t.Log.Debugf("Skipping missing values file [%s]: ignoreMissingValueFiles is set", ui.Cyan(entry))
	return true
}

// refFileFor validates one valueFiles entry and reports the ref-source file it
// names. isRef is false for a plain entry, which the caller resolves against
// the chart directory.
func (t *Target) refFileFor(entry string, refs map[string]*models.Source) (rf refFile, isRef bool, err error) {
	refName, rel, isRef := splitRefValueFile(entry)
	if !isRef {
		return refFile{}, false, validateRelValueFile(entry)
	}
	if _, ok := refs[refName]; !ok {
		return refFile{}, true, fmt.Errorf("%w: %q names ref %q, declared by no source of %s",
			ErrUnknownValueFileRef, entry, refName, t.App.Metadata.Name)
	}
	if err := validateRelValueFile(rel); err != nil {
		return refFile{}, true, err
	}
	// Normalized once here: the working-tree leg cleans the path on the way to
	// disk while a Git tree lookup matches the entry name exactly, so an unclean
	// entry would resolve on one leg and read as missing on the other.
	return refFile{Ref: refName, Path: path.Clean(filepath.ToSlash(rel))}, true, nil
}

// usedRefFiles lists the distinct ref-source files the renderable sources
// reference, so only those need materializing. Order is stable to keep
// materialization errors reproducible.
func (t *Target) usedRefFiles() ([]refFile, error) {
	var (
		used []refFile
		seen = make(map[refFile]bool)
	)
	refs, err := t.refSources()
	if err != nil {
		return nil, err
	}
	for _, source := range t.renderableSources() {
		for _, entry := range source.Helm.ValueFiles {
			rf, isRef, err := t.refFileFor(entry, refs)
			if err != nil {
				return nil, err
			}
			if !isRef || seen[rf] {
				continue
			}
			seen[rf] = true
			used = append(used, rf)
		}
	}
	return used, nil
}

// ignoresMissingRefFile reports whether every renderable source referencing rf
// sets helm.ignoreMissingValueFiles. One that does not must still fail, since
// its render would fail in ArgoCD too. Entries are validated before
// materialization, so a resolve error here is old news and never skips.
func (t *Target) ignoresMissingRefFile(rf refFile) bool {
	refs, err := t.refSources()
	if err != nil {
		return false
	}
	referenced := false
	for _, source := range t.renderableSources() {
		for _, entry := range source.Helm.ValueFiles {
			candidate, isRef, err := t.refFileFor(entry, refs)
			if err != nil || !isRef || candidate != rf {
				continue
			}
			if !source.Helm.IgnoreMissingValueFiles {
				return false
			}
			referenced = true
		}
	}
	return referenced
}

// renderableSources lists the sources that produce manifests, skipping
// values-only ref sources.
func (t *Target) renderableSources() []*models.Source {
	if t.App.Spec.MultiSource {
		out := make([]*models.Source, 0, len(t.App.Spec.Sources))
		for _, source := range t.App.Spec.Sources {
			if source.Renderable() {
				out = append(out, source)
			}
		}
		return out
	}
	if !t.App.Spec.Source.Renderable() {
		return nil
	}
	return []*models.Source{t.App.Spec.Source}
}

// validateRelValueFile rejects a valueFiles path that could escape the
// directory it is joined onto. The Application YAML is PR-author controlled, so
// an absolute or traversing path would otherwise read host files into the
// rendered diff posted as a merge request comment.
func validateRelValueFile(rel string) error {
	if rel == "" {
		return fmt.Errorf("%w: path must not be empty", ErrInvalidValueFilePath)
	}
	if filepath.IsAbs(rel) {
		return fmt.Errorf("%w: absolute paths are not allowed: %q", ErrInvalidValueFilePath, rel)
	}
	// Segment-wise, so a file legitimately named "..values.yaml" is not read as
	// a traversal.
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: path traversal is not allowed: %q", ErrInvalidValueFilePath, rel)
	}
	// A CI checkout keeps the credential it cloned with in .git/config, and no
	// values file legitimately lives in Git's own metadata.
	if first, _, _ := strings.Cut(filepath.ToSlash(cleaned), "/"); strings.EqualFold(first, ".git") {
		return fmt.Errorf("%w: Git metadata is not readable: %q", ErrInvalidValueFilePath, rel)
	}
	return nil
}
