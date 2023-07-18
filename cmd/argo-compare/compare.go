package main

import (
	"fmt"
	"github.com/codingsince1985/checksum"
	"github.com/fatih/color"
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/spf13/afero"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
)

type File struct {
	Name string
	Sha  string
}

type Compare struct {
	CmdRunner    utils.CmdRunner
	srcFiles     []File
	dstFiles     []File
	diffFiles    []File
	addedFiles   []File
	removedFiles []File
}

const (
	srcPathPattern          = "%s/templates/src/%s"
	dstPathPattern          = "%s/templates/dst/%s"
	currentFilePrintPattern = "▶ %s"
)

func (c *Compare) findFiles() {
	// Most of the time, we want to avoid huge output containing helm labels update only,
	// but we still want to be able to see the diff if needed
	if !preserveHelmLabels {
		c.findAndStripHelmLabels()
	}

	wg := new(sync.WaitGroup)
	wg.Add(2)

	go func() {
		defer wg.Done()
		if srcFiles, err := helpers.FindYamlFiles(filepath.Join(tmpDir, "templates/src")); err == nil {
			c.srcFiles = c.processFiles(srcFiles, "src")
		} else {
			log.Fatal(err)
		}
	}()

	go func() {
		defer wg.Done()
		if dstFiles, err := helpers.FindYamlFiles(filepath.Join(tmpDir, "templates/dst")); err == nil {
			c.dstFiles = c.processFiles(dstFiles, "dst")
		} else {
			// we are no longer failing here, because we need to support the case where the destination
			// branch does not have the Application yet
			log.Debugf("Error while finding files in %s: %s", filepath.Join(tmpDir, "templates/dst"), err)
		}
	}()

	wg.Wait()

	if !reflect.DeepEqual(c.srcFiles, c.dstFiles) {
		c.generateFilesStatus()
	}
}

func (c *Compare) processFiles(files []string, filesType string) []File {
	var processedFiles []File

	path := filepath.Join(tmpDir, "templates", filesType)

	for _, file := range files {
		if sha256sum, err := checksum.SHA256sum(file); err != nil {
			log.Fatal(err)
		} else {
			processedFiles = append(processedFiles, File{Name: strings.TrimPrefix(file, path), Sha: sha256sum})
		}
	}

	return processedFiles
}

func (c *Compare) generateFilesStatus() {
	c.findNewOrRemovedFiles()
	c.compareFiles()
}

func (c *Compare) compareFiles() {
	var diffFiles []File

	for _, srcFile := range c.srcFiles {
		for _, dstFile := range c.dstFiles {
			if srcFile.Name == dstFile.Name && srcFile.Sha != dstFile.Sha {
				diffFiles = append(diffFiles, srcFile)
			}
		}
	}

	c.diffFiles = diffFiles
}

// findNewOrRemovedFiles scans source and destination files to identify
// newly added or removed files. It populates the addedFiles and removedFiles
// fields of the Compare struct with the respective files. A file is considered
// added if it exists in the source but not in the destination, and removed if
// it exists in the destination but not in the source.
func (c *Compare) findNewOrRemovedFiles() {
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
		if _, found := srcFileMap[fileName]; !found {
			c.removedFiles = append(c.removedFiles, dstFile)
		}
	}
}

// printFilesStatus logs the status of the files processed during comparison.
// It determines whether files have been added, removed or have differences,
// and calls printFiles for each case.
func (c *Compare) printFilesStatus() {
	if len(c.addedFiles) == 0 && len(c.removedFiles) == 0 && len(c.diffFiles) == 0 {
		log.Info("No diff was found in rendered manifests!")
		return
	}

	c.printFiles(c.addedFiles, "added")
	c.printFiles(c.removedFiles, "removed")
	c.printFiles(c.diffFiles, "changed")
}

// printFiles logs the files that are subject to an operation (addition, removal, change).
// It logs the number of affected files and processes each file according to the operation.
func (c *Compare) printFiles(files []File, operation string) {
	if len(files) > 0 {
		fileText := "file"
		if len(files) > 1 {
			fileText = "files"
		}
		log.Infof("The following %d %s would be %s:", len(files), fileText, operation)
		for _, file := range files {
			c.processManifest(file, operation)
		}
	}
}

func (c *Compare) processManifest(file File, fileType string) {
	log.Infof(currentFilePrintPattern, file.Name)

	switch fileType {
	case "added":
		if printAddedManifests {
			color.Green(string(helpers.ReadFile(fmt.Sprintf(srcPathPattern, tmpDir, file.Name))))
		}
	case "removed":
		if printRemovedManifests {
			color.Red(string(helpers.ReadFile(fmt.Sprintf(dstPathPattern, tmpDir, file.Name))))
		}
	case "changed":
		c.printDiffFile(file)
	}
}

// printDiffFile determines the diff method (built-in or custom) to be used and then
// performs the file difference operation.
func (c *Compare) printDiffFile(diffFile File) {
	switch diffCommand {
	case "built-in":
		srcFile := string(helpers.ReadFile(fmt.Sprintf(srcPathPattern, tmpDir, diffFile.Name)))
		dstFile := string(helpers.ReadFile(fmt.Sprintf(dstPathPattern, tmpDir, diffFile.Name)))
		c.printBuiltInDiff(srcFile, dstFile)
	default:
		c.runCustomDiffCommand(diffFile)
	}
}

// printBuiltInDiff uses the built-in diff method to compare two files and prints
// the diff result. The diff method is provided by the diffmatchpatch package.
func (c *Compare) printBuiltInDiff(srcFile, dstFile string) {
	differ := diffmatchpatch.New()
	diffs := differ.DiffMain(dstFile, srcFile, false)

	log.Info(differ.DiffPrettyText(diffs))
}

// runCustomDiffCommand runs a custom diff command on two files and prints the
// result. It creates and runs the command using the os/exec package.
// If the command returns an error, it logs the error as a debug message.
func (c *Compare) runCustomDiffCommand(diffFile File) {
	command := fmt.Sprintf(diffCommand,
		fmt.Sprintf(dstPathPattern, tmpDir, diffFile.Name),
		fmt.Sprintf(srcPathPattern, tmpDir, diffFile.Name),
	)

	log.Debugf("Using custom diff command: %s", cyan(command))

	stdout, stderr, err := c.CmdRunner.Run("sh", "-c", command)
	if err != nil {
		// In some cases custom diff command might return non-zero exit code which is not an error
		// For example: diff -u file1 file2 returns 1 if files are different
		// Hence we are not failing here
		log.Error(stderr)
	}

	log.Info(stdout)
}

// findAndStripHelmLabels scans directory for YAML files, strips pre-defined Helm labels,
// and writes modified content back.
func (c *Compare) findAndStripHelmLabels() {
	var helmFiles []string
	var err error

	if helmFiles, err = helpers.FindYamlFiles(tmpDir); err != nil {
		log.Fatal(err)
	}

	for _, helmFile := range helmFiles {
		if desiredState, err := helpers.StripHelmLabels(helmFile); err != nil {
			log.Fatal(err)
		} else {
			if err := helpers.WriteToFile(afero.NewOsFs(), helmFile, desiredState); err != nil {
				log.Fatal(err)
			}
		}
	}
}
