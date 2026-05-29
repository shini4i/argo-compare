package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/spf13/afero"
)

// MaterializeTreeDir writes every file in the subpath of tree to destDir,
// preserving the relative directory layout and the recorded file mode.
//
// subpath is resolved relative to tree's root. Use "" or "." to materialize
// the entire tree. The destination directory is created if missing; existing
// files at the destination are overwritten.
//
// MaterializeTreeDir returns an error if subpath does not resolve to a
// directory inside the tree (including the case where it points at a blob),
// or if the context is cancelled before or during the walk.
func MaterializeTreeDir(ctx context.Context, fs afero.Fs, tree *object.Tree, subpath, destDir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	sub := tree
	if subpath != "" && subpath != "." {
		t, err := tree.Tree(subpath)
		if err != nil {
			return fmt.Errorf("resolve subpath %q in tree: %w", subpath, err)
		}
		sub = t
	}

	if err := fs.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create destination %q: %w", destDir, err)
	}

	destClean := filepath.Clean(destDir)
	iter := sub.Files()
	return iter.ForEach(func(file *object.File) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		abs := filepath.Clean(filepath.Join(destClean, file.Name))
		// Defense-in-depth: reject any tree entry whose materialized path
		// escapes destDir. Standard git rejects `..` and `.` as entry names,
		// so this should be unreachable for trees produced by `git commit`,
		// but a hand-crafted malicious tree could otherwise write outside
		// destDir when invoked in CI.
		if abs != destClean && !strings.HasPrefix(abs, destClean+string(filepath.Separator)) {
			return fmt.Errorf("tree entry %q escapes destination", file.Name)
		}
		if err := fs.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return fmt.Errorf("create dir for %q: %w", abs, err)
		}
		content, err := file.Contents()
		if err != nil {
			return fmt.Errorf("read blob %q: %w", file.Name, err)
		}
		mode, err := file.Mode.ToOSFileMode()
		if err != nil {
			mode = 0o644
		}
		if err := afero.WriteFile(fs, abs, []byte(content), mode); err != nil {
			return fmt.Errorf("write %q: %w", abs, err)
		}
		return nil
	})
}
