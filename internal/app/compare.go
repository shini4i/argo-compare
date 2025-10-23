package app

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
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/spf13/afero"
)

type File struct {
	Name string
	Sha  string
}

type Compare struct {
	Globber               ports.Globber
	ExternalDiffTool      string
	TmpDir                string
	PreserveHelmLabels    bool
	PrintAddedManifests   bool
	PrintRemovedManifests bool
	Log                   *logging.Logger

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
	if !c.PreserveHelmLabels {
		c.findAndStripHelmLabels()
	}

	wg := new(sync.WaitGroup)
	wg.Add(2)

	go func() {
		defer wg.Done()
		globPattern := filepath.Join(filepath.Join(filepath.Join(c.TmpDir, "templates/src"), "**", "*.yaml"))
		srcFiles, err := c.Globber.Glob(globPattern)
		if err != nil {
			c.Log.Fatal(err)
		}
		c.srcFiles = c.processFiles(srcFiles, "src")
	}()

	go func() {
		defer wg.Done()
		globPattern := filepath.Join(filepath.Join(filepath.Join(c.TmpDir, "templates/dst"), "**", "*.yaml"))
		if dstFiles, err := c.Globber.Glob(globPattern); err != nil {
			c.Log.Debugf("Error while finding files in %s: %s", filepath.Join(c.TmpDir, "templates/dst"), err)
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

	path := filepath.Join(c.TmpDir, "templates", filesType)

	for _, file := range files {
		if sha256sum, err := checksum.SHA256sum(file); err != nil {
			c.Log.Fatal(err)
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

func (c *Compare) findNewOrRemovedFiles() {
	srcFileMap := make(map[string]File)
	for _, srcFile := range c.srcFiles {
		srcFileMap[srcFile.Name] = srcFile
	}

	dstFileMap := make(map[string]File)
	for _, dstFile := range c.dstFiles {
		dstFileMap[dstFile.Name] = dstFile
	}

	var addedFiles []File
	var removedFiles []File

	for fileName, srcFile := range srcFileMap {
		if _, found := dstFileMap[fileName]; !found {
			addedFiles = append(addedFiles, srcFile)
		}
	}

	for fileName, dstFile := range dstFileMap {
		if _, found := srcFileMap[fileName]; !found {
			removedFiles = append(removedFiles, dstFile)
		}
	}

	c.addedFiles = addedFiles
	c.removedFiles = removedFiles
}

func (c *Compare) printFilesStatus() {
	if len(c.addedFiles) == 0 && len(c.removedFiles) == 0 && len(c.diffFiles) == 0 {
		c.Log.Info("No diff was found in rendered manifests!")
		return
	}

	if c.PrintAddedManifests {
		c.printFiles(c.addedFiles, "added")
	}
	if c.PrintRemovedManifests {
		c.printFiles(c.removedFiles, "removed")
	}
	c.printFiles(c.diffFiles, "changed")
}

func (c *Compare) printFiles(files []File, operation string) {
	if len(files) == 0 {
		return
	}

	fileText := "file"
	if len(files) > 1 {
		fileText = "files"
	}
	c.Log.Infof("The following %d %s would be %s:", len(files), fileText, operation)
	for _, file := range files {
		c.Log.Infof(currentFilePrintPattern, file.Name)
		c.printDiffFile(file)
	}
}

func (c *Compare) printDiffFile(diffFile File) {
	dstFilePath := fmt.Sprintf(dstPathPattern, c.TmpDir, diffFile.Name)
	srcFilePath := fmt.Sprintf(srcPathPattern, c.TmpDir, diffFile.Name)

	srcFile := string(readFile(srcFilePath))
	dstFile := string(readFile(dstFilePath))

	edits := myers.ComputeEdits(span.URIFromPath(srcFilePath), dstFile, srcFile)

	output := fmt.Sprint(gotextdiff.ToUnified(srcFilePath, dstFilePath, dstFile, edits))

	if c.ExternalDiffTool != "" {
		cmd := exec.Command(c.ExternalDiffTool) // #nosec G204

		cmd.Stdin = strings.NewReader(output)

		cmdOutput, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Println("Error running external program:", err)
		}

		fmt.Println(string(cmdOutput))
	} else {
		fmt.Println(output)
	}
}

func (c *Compare) findAndStripHelmLabels() {
	var helmFiles []string
	var err error

	if helmFiles, err = c.Globber.Glob(filepath.Join(c.TmpDir, "**", "*.yaml")); err != nil {
		c.Log.Fatal(err)
	}

	for _, helmFile := range helmFiles {
		if desiredState, err := helpers.StripHelmLabels(helmFile); err != nil {
			c.Log.Fatal(err)
		} else {
			if err := helpers.WriteToFile(afero.NewOsFs(), helmFile, desiredState); err != nil {
				c.Log.Fatal(err)
			}
		}
	}
}
