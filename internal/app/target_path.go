package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/shini4i/argo-compare/internal/models"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/spf13/afero"
)

// ErrMixedMultiSource is returned when an Application's `sources` mixes
// registry-based (chart) and Git-path-based (path) entries. The first version
// of path-based support requires that every source in a multi-source
// Application use the same kind, so the renderer can pick a single code path.
var ErrMixedMultiSource = errors.New("multi-source Application mixes chart-based and path-based sources; mixed sources are not supported")

// effectiveChartName returns a non-empty chart name suitable for naming the
// extracted/materialized chart directory and the per-source values file.
// Registry sources use Source.Chart directly; path-based sources fall back to
// the last component of Source.Path (without a trailing slash).
func effectiveChartName(s *models.Source) string {
	if s == nil {
		return ""
	}
	if s.Chart != "" {
		return s.Chart
	}
	trimmed := strings.TrimRight(s.Path, "/")
	if trimmed == "" {
		return ""
	}
	return filepath.Base(trimmed)
}

// PathBased reports whether the Application uses Git path sources. A
// multi-source Application is path-based if all of its sources are path-based;
// callers should invoke ClassifySources first to reject mixed configurations.
func (t *Target) PathBased() bool {
	if t.App.Spec.MultiSource {
		if len(t.App.Spec.Sources) == 0 {
			return false
		}
		for _, s := range t.App.Spec.Sources {
			if s == nil || s.Path == "" {
				return false
			}
		}
		return true
	}
	return t.App.Spec.Source != nil && t.App.Spec.Source.Path != ""
}

// ClassifySources rejects multi-source Applications that mix registry and
// path sources. Single-source Applications and uniform multi-source
// Applications return nil.
func (t *Target) ClassifySources() error {
	if !t.App.Spec.MultiSource {
		return nil
	}
	var (
		seenChart bool
		seenPath  bool
	)
	for _, s := range t.App.Spec.Sources {
		if s == nil {
			continue
		}
		if s.Chart != "" {
			seenChart = true
		}
		if s.Path != "" {
			seenPath = true
		}
	}
	if seenChart && seenPath {
		return ErrMixedMultiSource
	}
	return nil
}

// MaterializeChartFromWorkingTree copies the chart directory referenced by each
// path-based source from the local working tree into the same on-disk layout
// the registry pipeline produces (TmpDir/charts/<TargetType>/<ChartName>). It
// is only meaningful for source-side rendering (t.Type == TargetTypeSource);
// the destination side reads from a Git tree via MaterializeChartFromTree.
func (t *Target) MaterializeChartFromWorkingTree(ctx context.Context, fs afero.Fs, repoRoot string) error {
	for _, src := range t.pathSources() {
		if err := ctx.Err(); err != nil {
			return err
		}
		from := filepath.Join(repoRoot, src.Path)
		to := filepath.Join(t.TmpDir, "charts", t.Type, effectiveChartName(src))
		if err := copyDirOnDisk(fs, from, to); err != nil {
			return fmt.Errorf("materialize chart %q from working tree: %w", src.Path, err)
		}
	}
	return nil
}

// MaterializeChartFromTree extracts the chart directory referenced by each
// path-based source from tree into TmpDir/charts/<TargetType>/<ChartName>.
// It is the destination-side counterpart of MaterializeChartFromWorkingTree
// and uses MaterializeTreeDir under the hood for the actual walk.
func (t *Target) MaterializeChartFromTree(ctx context.Context, fs afero.Fs, tree *object.Tree) error {
	for _, src := range t.pathSources() {
		dest := filepath.Join(t.TmpDir, "charts", t.Type, effectiveChartName(src))
		if err := MaterializeTreeDir(ctx, fs, tree, src.Path, dest); err != nil {
			return fmt.Errorf("materialize chart %q from tree: %w", src.Path, err)
		}
	}
	return nil
}

// pathSources enumerates the path-based sources for the Application,
// transparently handling the single-source / multi-source split.
func (t *Target) pathSources() []*models.Source {
	if t.App.Spec.MultiSource {
		return t.App.Spec.Sources
	}
	if t.App.Spec.Source == nil {
		return nil
	}
	return []*models.Source{t.App.Spec.Source}
}

// copyDirOnDisk recursively copies the contents of src into dst using fs.
// It is intentionally minimal: no symlink handling, no permissions beyond what
// os.Stat reports, no atomicity. The caller owns the destination directory.
func copyDirOnDisk(fs afero.Fs, src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("source %q is not a directory", src)
	}
	if err := fs.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return fs.MkdirAll(target, 0o755)
		}
		return copyFile(fs, path, target)
	})
}

func copyFile(fs afero.Fs, src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- src is derived from a path inside the local repo
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	if err := fs.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := fs.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}
