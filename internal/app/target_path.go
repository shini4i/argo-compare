package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/spf13/afero"
)

// ErrMixedMultiSource is returned when an Application's `sources` mixes
// registry-based (chart) and Git-path-based (path) entries. The first version
// of path-based support requires that every source in a multi-source
// Application use the same kind, so the renderer can pick a single code path.
var ErrMixedMultiSource = errors.New("multi-source Application mixes chart-based and path-based sources; mixed sources are not supported")

// ErrChartPathNotInTree is returned by MaterializeChartFromTree when a
// path-based source's chart directory does not exist in the provided Git tree.
// This happens when the chart is added for the first time on the current
// branch: the working tree has it (source leg) but the merge-base tree does
// not (destination leg). The anchor flow treats this as a newly added
// Application with no baseline to diff against, mirroring errGitFileDoesNotExist
// in the registry-chart flow.
var ErrChartPathNotInTree = errors.New("chart path does not exist in target tree")

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
//
// spec.source.path is treated as untrusted (a malicious or misconfigured
// Application can set it to "../../etc" or an absolute path); resolveRepoPath
// rejects anything that escapes repoRoot before any I/O happens. The
// destination leg does not need the same guard because go-git tree walks
// cannot contain ".." entries.
func (t *Target) MaterializeChartFromWorkingTree(ctx context.Context, fs afero.Fs, repoRoot string) error {
	for _, src := range t.pathSources() {
		if err := ctx.Err(); err != nil {
			return err
		}
		from, err := resolveRepoPath(repoRoot, src.Path)
		if err != nil {
			return fmt.Errorf("materialize chart %q from working tree: %w", src.Path, err)
		}
		to := filepath.Join(t.TmpDir, "charts", t.Type, effectiveChartName(src))
		if err := copyDirOnDisk(fs, from, to); err != nil {
			return fmt.Errorf("materialize chart %q from working tree: %w", src.Path, err)
		}
	}
	return nil
}

// resolveRepoPath joins rel onto repoRoot and rejects results that escape
// repoRoot via ".." segments. It is the shared defense against untrusted-path
// inputs from anchor / Application YAML on the local-filesystem code paths
// (MaterializeChartFromWorkingTree, RealApplicationFetcher.fetchFromLocal).
// The matching defense on the Git-tree code paths lives inside
// MaterializeTreeDir; go-git tree entries cannot themselves contain "..".
//
// Note: an absolute rel (e.g. "/etc/passwd") is rebased under repoRoot by
// filepath.Join and is therefore not an escape vector — it resolves to a
// nonsense path inside the repo that will fail benignly at later I/O.
func resolveRepoPath(repoRoot, rel string) (string, error) {
	rootClean := filepath.Clean(repoRoot)
	abs := filepath.Clean(filepath.Join(rootClean, rel))
	if abs != rootClean && !strings.HasPrefix(abs, rootClean+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes repository root", rel)
	}
	return abs, nil
}

// MaterializeChartFromTree extracts the chart directory referenced by each
// path-based source from tree into TmpDir/charts/<TargetType>/<ChartName>.
// It is the destination-side counterpart of MaterializeChartFromWorkingTree
// and uses MaterializeTreeDir under the hood for the actual walk.
//
// A source whose path is absent from tree yields ErrChartPathNotInTree (wrapped
// with the offending path) rather than go-git's opaque ErrDirectoryNotFound, so
// the anchor flow can recognize a newly added chart and treat it as a new
// Application instead of failing the run.
func (t *Target) MaterializeChartFromTree(ctx context.Context, fs afero.Fs, tree *object.Tree) error {
	for _, src := range t.pathSources() {
		dest := filepath.Join(t.TmpDir, "charts", t.Type, effectiveChartName(src))
		if err := MaterializeTreeDir(ctx, fs, tree, src.Path, dest); err != nil {
			if errors.Is(err, object.ErrDirectoryNotFound) {
				return fmt.Errorf("%w: %s", ErrChartPathNotInTree, src.Path)
			}
			return fmt.Errorf("materialize chart %q from tree: %w", src.Path, err)
		}
	}
	return nil
}

// BuildChartDependencies runs `helm dependency build` against each path-based
// source's chart directory so subcharts declared in Chart.yaml are available
// to `helm template`. Charts without dependencies (or without a Chart.yaml at
// all) are skipped silently inside the processor.
//
// This step has no counterpart for registry-based sources: chart tarballs
// pulled from a registry already ship their dependencies in charts/.
func (t *Target) BuildChartDependencies(ctx context.Context) error {
	deps := ports.HelmDeps{
		CmdRunner:           t.CmdRunner,
		Globber:             t.Globber,
		CredentialProviders: t.CredentialProviders,
	}
	for _, src := range t.pathSources() {
		if err := ctx.Err(); err != nil {
			return err
		}
		chartDir := filepath.Join(t.TmpDir, "charts", t.Type, effectiveChartName(src))
		if err := t.HelmProcessor.BuildChartDependencies(ctx, deps, chartDir, t.TmpDir); err != nil {
			return fmt.Errorf("build dependencies for chart %q: %w", src.Path, err)
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

// copyDirOnDisk recursively copies the contents of src into dst using dstFs.
// Only regular files and directories are accepted; symlinks, FIFOs, sockets,
// and devices are rejected. Symlinks would let an attacker-controlled chart
// leak files outside the chart dir into rendered manifests (and the Git-tree
// dst-leg counterpart cannot mirror them faithfully — it records the link
// target, not the resolved content, producing misleading diffs). FIFOs and
// sockets would hang copyFile's os.Open indefinitely on CI runners. Charts
// wanting shared content can commit the file directly. The caller owns the
// destination directory.
func copyDirOnDisk(dstFs afero.Fs, src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("source %q is not a regular directory (mode %s); only regular files and directories are supported in path-based chart sources", src, info.Mode())
	}
	if err := dstFs.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && d.Type()&fs.ModeType != 0 {
			return fmt.Errorf("source %q contains %q with unsupported mode %s; only regular files and directories are supported in path-based chart sources", src, path, d.Type())
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return dstFs.MkdirAll(target, 0o755)
		}
		return copyFile(dstFs, path, target)
	})
}

func copyFile(dstFs afero.Fs, src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- src is constrained to the local repo by resolveRepoPath; symlinks are rejected in copyDirOnDisk
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	if err := dstFs.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := dstFs.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}
