package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shini4i/argo-compare/internal/models"
)

// ErrIndistinguishableSources is returned when two renderable sources of one
// multi-source Application share a repoURL, chart, path and releaseName.
// Nothing else — targetRevision or values — separates them on disk.
var ErrIndistinguishableSources = errors.New("two sources would render into the same directory")

// slotLength is how many hex characters of the identity digest name a slot.
// Four bytes separate any realistic number of sources in one Application.
const slotLength = 8

// sourceSlot names the per-source directory that keeps two sources of one
// Application out of each other's chart, manifest and values files. It is
// empty for a single-source Application, which has nothing to disambiguate.
func (t *Target) sourceSlot(source *models.Source) string {
	if source == nil || !t.App.Spec.MultiSource {
		return ""
	}
	// targetRevision is excluded on purpose: the legs render one source at
	// different revisions, and a slot moving with it would report every
	// manifest as removed and re-added. releaseName is in because helm already
	// nests output under it, so one chart can render twice under two names.
	parts := []string{source.RepoURL, source.Chart, source.Path, t.effectiveReleaseName(source)}
	// NUL-separated so the fields cannot run together: a chart and a path of
	// the same name would otherwise hash alike.
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:slotLength]
}

// effectiveReleaseName is the release Helm renders a source under, which falls
// back to the Application's name. The slot hashes this rather than the raw
// field so that spelling the fallback out explicitly does not move the source.
func (t *Target) effectiveReleaseName(source *models.Source) string {
	if source != nil && source.Helm.ReleaseName != "" {
		return source.Helm.ReleaseName
	}
	return t.App.Metadata.Name
}

// sourceDirFor is the workspace root a single source materializes under.
func (t *Target) sourceDirFor(source *models.Source) string {
	return filepath.Join(t.TmpDir, "charts", t.Type, t.sourceSlot(source))
}

// chartDirFor is the directory a renderable source's chart is materialized
// into, matching the layout produced by chart extraction.
func (t *Target) chartDirFor(source *models.Source) string {
	return filepath.Join(t.sourceDirFor(source), effectiveChartName(source))
}

// outputDirFor is the directory `helm template --output-dir` renders a source's
// manifests into. Their paths relative to templates/<leg> are the keys the
// comparison diffs on, so this must match on both legs of one source.
func (t *Target) outputDirFor(source *models.Source) string {
	return filepath.Join(t.TmpDir, "templates", t.Type, t.sourceSlot(source))
}

// inlineValuesFileFor is where a source's helm.values / helm.valuesObject are
// written for this leg.
func (t *Target) inlineValuesFileFor(source *models.Source) string {
	name := effectiveChartName(source) + "-values.yaml"
	return filepath.Join(t.TmpDir, "values", t.Type, t.sourceSlot(source), name)
}

// checkDistinctSources rejects renderable sources the slot cannot separate: any
// two sharing a repoURL, chart, path and releaseName. Left alone they would
// unpack over each other and render the same chart twice, silently.
func (t *Target) checkDistinctSources() error {
	seen := make(map[string]*models.Source)
	for _, source := range t.renderableSources() {
		slot := t.sourceSlot(source)
		if first, dup := seen[slot]; dup {
			return fmt.Errorf("%w: %q and %q in %s",
				ErrIndistinguishableSources,
				sourceLabel(first), sourceLabel(source), t.App.Metadata.Name)
		}
		seen[slot] = source
	}
	return nil
}

// sourceLabel identifies a source in an error message. The repoURL is redacted
// because an Application may carry userinfo in it and this error reaches CI logs.
func sourceLabel(source *models.Source) string {
	ref := source.Chart
	if ref == "" {
		ref = source.Path
	}
	return redactRepo(source.RepoURL) + "/" + ref
}
