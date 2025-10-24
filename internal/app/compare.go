package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/codingsince1985/checksum"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/spf13/afero"
)

// File captures the relative name and checksum of a rendered manifest.
type File struct {
	Name string
	Sha  string
}

// DiffOutput contains the unified diff for a single manifest.
type DiffOutput struct {
	File File
	Diff string
}

// ComparisonResult aggregates the additions, removals, and changes discovered.
type ComparisonResult struct {
	Added   []DiffOutput
	Removed []DiffOutput
	Changed []DiffOutput
}

// IsEmpty reports whether there are no changes to present.
func (r ComparisonResult) IsEmpty() bool {
	return len(r.Added) == 0 && len(r.Removed) == 0 && len(r.Changed) == 0
}

// Compare analyses rendered manifest trees to produce diff results.
const yamlGlob = "*.yaml"

type Compare struct {
	Globber            ports.Globber
	TmpDir             string
	PreserveHelmLabels bool

	srcFiles     []File
	dstFiles     []File
	addedFiles   []File
	removedFiles []File
	diffFiles    []File
}

// Execute orchestrates the comparison of rendered manifests.
func (c *Compare) Execute() (ComparisonResult, error) {
	if err := c.prepareFiles(); err != nil {
		return ComparisonResult{}, err
	}

	c.generateFilesStatus()

	return c.buildResult()
}

// prepareFiles normalizes render outputs and populates source/destination file lists.
func (c *Compare) prepareFiles() error {
	if !c.PreserveHelmLabels {
		if err := c.stripHelmLabels(); err != nil {
			return err
		}
	}

	srcPattern := filepath.Join(c.TmpDir, "templates", "src", "**", yamlGlob)
	srcFiles, err := c.Globber.Glob(srcPattern)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		srcFiles = nil
	}
	c.srcFiles, err = c.processFiles(srcFiles, "src")
	if err != nil {
		return err
	}

	dstPattern := filepath.Join(c.TmpDir, "templates", "dst", "**", yamlGlob)
	dstFiles, err := c.Globber.Glob(dstPattern)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		dstFiles = nil
	}
	c.dstFiles, err = c.processFiles(dstFiles, "dst")
	if err != nil {
		return err
	}

	return nil
}

// processFiles records manifest metadata for the supplied file set.
func (c *Compare) processFiles(files []string, filesType string) ([]File, error) {
	var processedFiles []File

	path := filepath.Join(c.TmpDir, "templates", filesType)

	for _, file := range files {
		sha256sum, err := checksum.SHA256sum(file)
		if err != nil {
			return nil, err
		}
		processedFiles = append(processedFiles, File{Name: strings.TrimPrefix(file, path), Sha: sha256sum})
	}

	return processedFiles, nil
}

// generateFilesStatus computes the sets of added, removed, and changed manifests.
func (c *Compare) generateFilesStatus() {
	srcFileMap := make(map[string]File)
	for _, srcFile := range c.srcFiles {
		srcFileMap[srcFile.Name] = srcFile
	}

	dstFileMap := make(map[string]File)
	for _, dstFile := range c.dstFiles {
		dstFileMap[dstFile.Name] = dstFile
	}

	for fileName, srcFile := range srcFileMap {
		if _, found := dstFileMap[fileName]; !found {
			c.addedFiles = append(c.addedFiles, srcFile)
		}
	}

	for fileName, dstFile := range dstFileMap {
		if srcFile, found := srcFileMap[fileName]; found {
			if srcFile.Sha != dstFile.Sha {
				c.diffFiles = append(c.diffFiles, srcFile)
			}
		} else {
			c.removedFiles = append(c.removedFiles, dstFile)
		}
	}
}

// buildResult produces the final comparison result with generated diffs.
func (c *Compare) buildResult() (ComparisonResult, error) {
	added, err := c.generateDiffs(c.addedFiles)
	if err != nil {
		return ComparisonResult{}, err
	}

	removed, err := c.generateDiffs(c.removedFiles)
	if err != nil {
		return ComparisonResult{}, err
	}

	changed, err := c.generateDiffs(c.diffFiles)
	if err != nil {
		return ComparisonResult{}, err
	}

	return ComparisonResult{
		Added:   added,
		Removed: removed,
		Changed: changed,
	}, nil
}

// generateDiffs collects unified diff outputs for each provided file.
func (c *Compare) generateDiffs(files []File) ([]DiffOutput, error) {
	var outputs []DiffOutput

	for _, f := range files {
		diff, err := c.generateDiff(f)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, DiffOutput{File: f, Diff: diff})
	}

	return outputs, nil
}

// generateDiff creates the unified diff for a single manifest entry.
func (c *Compare) generateDiff(f File) (string, error) {
	dstFilePath := filepath.Join(c.TmpDir, "templates", "dst", f.Name)
	srcFilePath := filepath.Join(c.TmpDir, "templates", "src", f.Name)

	srcFile, err := readFileContent(srcFilePath)
	if err != nil {
		return "", err
	}
	dstFile, err := readFileContent(dstFilePath)
	if err != nil {
		return "", err
	}

	edits := myers.ComputeEdits(span.URIFromPath(srcFilePath), string(dstFile), string(srcFile))

	return fmt.Sprint(gotextdiff.ToUnified(srcFilePath, dstFilePath, string(dstFile), edits)), nil
}

// stripHelmLabels removes Helm-managed metadata that would otherwise produce noisy diffs.
func (c *Compare) stripHelmLabels() error {
	helmFiles, err := c.Globber.Glob(filepath.Join(c.TmpDir, "**", yamlGlob))
	if err != nil {
		return err
	}

	for _, helmFile := range helmFiles {
		desiredState, err := helpers.StripHelmLabels(helmFile)
		if err != nil {
			return err
		}
		if err := helpers.WriteToFile(afero.NewOsFs(), helmFile, desiredState); err != nil {
			return err
		}
	}

	return nil
}

// readFileContent loads file contents while tolerating missing files.
func readFileContent(path string) ([]byte, error) {
	data, err := os.ReadFile(path) // #nosec G304
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}
