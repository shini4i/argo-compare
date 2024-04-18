package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/codingsince1985/checksum"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	interfaces "github.com/shini4i/argo-compare/cmd/argo-compare/interfaces"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/spf13/afero"
)

type File struct {
	Name string
	Sha  string
}

type Compare struct {
	CmdRunner        interfaces.CmdRunner
	Globber          utils.CustomGlobber
	externalDiffTool string
	srcFiles         []File
	dstFiles         []File
	diffFiles        []File
	addedFiles       []File
	removedFiles     []File
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
		globPattern := filepath.Join(filepath.Join(filepath.Join(tmpDir, "templates/src"), "**", "*.yaml"))
		srcFiles, err := c.Globber.Glob(globPattern)
		if err != nil {
			log.Fatal(err)
		}
		c.srcFiles = c.processFiles(srcFiles, "src")
	}()

	go func() {
		defer wg.Done()
		globPattern := filepath.Join(filepath.Join(filepath.Join(tmpDir, "templates/dst"), "**", "*.yaml"))
		if dstFiles, err := c.Globber.Glob(globPattern); err != nil {
			// we are no longer failing here, because we need to support the case where the destination
			// branch does not have the Application yet
			log.Debugf("Error while finding files in %s: %s", filepath.Join(tmpDir, "templates/dst"), err)
		} else {
			c.dstFiles = c.processFiles(dstFiles, "dst")
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
			log.Infof(currentFilePrintPattern, file.Name)
			c.printDiffFile(file)
		}
	}
}

func (c *Compare) printDiffFile(diffFile File) {
	dstFilePath := fmt.Sprintf(dstPathPattern, tmpDir, diffFile.Name)
	srcFilePath := fmt.Sprintf(srcPathPattern, tmpDir, diffFile.Name)

	srcFile := string(ReadFile(srcFilePath))
	dstFile := string(ReadFile(dstFilePath))

	edits := myers.ComputeEdits(span.URIFromPath(srcFilePath), dstFile, srcFile)

	output := fmt.Sprint(gotextdiff.ToUnified(srcFilePath, dstFilePath, dstFile, edits))

	if c.externalDiffTool != "" {
		cmd := exec.Command(c.externalDiffTool) // #nosec G204

		// Set the external program's stdin to read from a pipe
		cmd.Stdin = strings.NewReader(output)

		// Capture the output of the external program
		cmdOutput, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Println("Error running external program:", err)
		}

		// Print the external program's output
		fmt.Println(string(cmdOutput))
	} else {
		fmt.Println(output)
	}
}

// findAndStripHelmLabels scans directory for YAML files, strips pre-defined Helm labels,
// and writes modified content back.
func (c *Compare) findAndStripHelmLabels() {
	var helmFiles []string
	var err error

	if helmFiles, err = c.Globber.Glob(filepath.Join(tmpDir, "**", "*.yaml")); err != nil {
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
