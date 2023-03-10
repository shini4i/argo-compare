package main

import (
	"fmt"
	"github.com/codingsince1985/checksum"
	"github.com/fatih/color"
	"github.com/op/go-logging"
	"github.com/r3labs/diff/v3"
	"github.com/sergi/go-diff/diffmatchpatch"
	h "github.com/shini4i/argo-compare/internal/helpers"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
)

type File struct {
	Name string `diff:"name"`
	Sha  string `diff:"sha"`
}

type Compare struct {
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
	if srcFiles, err := h.FindYamlFiles(filepath.Join(tmpDir, "templates/src")); err == nil {
		c.srcFiles = c.processFiles(srcFiles, "src")
	} else {
		log.Fatal(err)
	}

	if dstFiles, err := h.FindYamlFiles(filepath.Join(tmpDir, "templates/dst")); err == nil {
		c.dstFiles = c.processFiles(dstFiles, "dst")
	} else {
		// we are no longer failing here, because we need to support the case where the destination
		// branch does not have the Application yet
		log.Debugf("Error while finding files in %s: %s", filepath.Join(tmpDir, "templates/dst"), err)
	}

	if !reflect.DeepEqual(c.srcFiles, c.dstFiles) {
		c.generateFilesStatus()
	}
}

func (c *Compare) processFiles(files []string, filesType string) []File {
	var processedFiles []File

	// Most of the time, we want to avoid huge output containing helm labels update only,
	// but we still want to be able to see the diff if needed
	if !preserveHelmLabels {
		c.findAndStripHelmLabels()
	}

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

// generateFilesStatus generates the `addedFiles`, `removedFiles`, and `diffFiles` slices
// by comparing the `srcFiles` and `dstFiles` slices. For each element in the `Diff` output,
// the function calls `handleCreate`, `handleDelete`, or `handleUpdate` based on the type of change.
func (c *Compare) generateFilesStatus() {
	filesStatus, err := diff.Diff(c.dstFiles, c.srcFiles)
	if err != nil {
		log.Fatal(err)
	}

	for _, fileStatus := range filesStatus {
		switch fileStatus.Type {
		case "create":
			c.handleCreate(fileStatus)
		case "delete":
			c.handleDelete(fileStatus)
		case "update":
			c.handleUpdate(fileStatus)
		}
	}
}

// handleCreate adds a file to the `addedFiles` slice if it has been created in the
// `dstFiles` slice. The function checks the `Path` field of the `Change` object to ensure
// that the created element is a file name. If the element is not a file name, the function
// returns without adding the file to the `addedFiles` slice.
func (c *Compare) handleCreate(fileStatus diff.Change) {
	if fileStatus.Path[1] != "name" {
		return
	}
	c.addedFiles = append(c.addedFiles, File{Name: fileStatus.To.(string)})
}

// handleDelete checks the `Path` field of the `Change` object to ensure
// that the deleted element is a file name in the `File` struct. If the element
// is not a file name, the function returns without adding the file to the
// `removedFiles` slice.
func (c *Compare) handleDelete(fileStatus diff.Change) {
	if fileStatus.Path[1] != "name" {
		return
	}
	c.removedFiles = append(c.removedFiles, File{Name: fileStatus.From.(string)})
}

// handleUpdate adds a file to the `diffFiles` slice if it has changed between the
// `c.srcFiles` and `c.dstFiles` slices, based on the `Change` object returned by the `Diff` function.
func (c *Compare) handleUpdate(fileStatus diff.Change) {
	for i := range c.dstFiles {
		if c.dstFiles[i].Sha == fileStatus.From.(string) {
			// If the SHA value of the file matches the target SHA value, add the file to the `diffFiles` slice
			c.diffFiles = append(c.diffFiles, c.dstFiles[i])
			break
		}
	}
}

func (c *Compare) printFilesStatus() {
	if len(c.addedFiles) == 0 && len(c.removedFiles) == 0 && len(c.diffFiles) == 0 {
		log.Info("No diff was found in rendered manifests!")
		return
	}

	if len(c.addedFiles) > 0 {
		log.Infof("The following %d file/files would be added:", len(c.addedFiles))
		for _, addedFile := range c.addedFiles {
			log.Infof(currentFilePrintPattern, addedFile.Name)
			if printAddedManifests {
				color.Green(string(
					h.ReadFile(fmt.Sprintf(srcPathPattern, tmpDir, addedFile.Name))),
				)
			}
		}
	}

	if len(c.removedFiles) > 0 {
		log.Infof("The following %d file/files would be removed:", len(c.removedFiles))
		for _, removedFile := range c.removedFiles {
			log.Infof(currentFilePrintPattern, removedFile.Name)
			if printRemovedManifests {
				color.Red(string(
					h.ReadFile(fmt.Sprintf(dstPathPattern, tmpDir, removedFile.Name))),
				)
			}
		}
	}

	if len(c.diffFiles) > 0 {
		log.Infof("The following %d file/files would be changed:", len(c.diffFiles))
		for _, diffFile := range c.diffFiles {
			log.Infof(currentFilePrintPattern, diffFile.Name)
			c.printDiffFile(diffFile)
		}

	}
}

func (c *Compare) printDiffFile(diffFile File) {
	switch diffCommand {
	case "built-in":
		differ := diffmatchpatch.New()

		srcFile := string(h.ReadFile(fmt.Sprintf(srcPathPattern, tmpDir, diffFile.Name)))
		dstFile := string(h.ReadFile(fmt.Sprintf(dstPathPattern, tmpDir, diffFile.Name)))

		diffs := differ.DiffMain(dstFile, srcFile, false)

		log.Info(differ.DiffPrettyText(diffs))
	default:
		command := fmt.Sprintf(diffCommand,
			fmt.Sprintf(dstPathPattern, tmpDir, diffFile.Name),
			fmt.Sprintf(srcPathPattern, tmpDir, diffFile.Name),
		)

		log.Debugf("Using custom diff command: %s", command)

		cmd := exec.Command("sh", "-c", command)
		cmd.Stdout = os.Stdout

		if logging.GetLevel(loggerName) == logging.DEBUG {
			cmd.Stderr = os.Stderr
		}

		if err := cmd.Run(); err != nil {
			// In some cases custom diff command might return non-zero exit code which is not an error
			// For example: diff -u file1 file2 returns 1 if files are different
			// Hence we are not failing here
			log.Debug(err.Error())
		}
	}
}

func (c *Compare) findAndStripHelmLabels() {
	helmFiles, err := h.FindYamlFiles(tmpDir)
	if err != nil {
		panic(err)
	}

	for _, helmFile := range helmFiles {
		if err := h.StripHelmLabels(helmFile); err != nil {
			panic(err)
		}
	}
}
