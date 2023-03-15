package main

import (
	"fmt"
	"github.com/codingsince1985/checksum"
	"github.com/fatih/color"
	"github.com/op/go-logging"
	"github.com/sergi/go-diff/diffmatchpatch"
	h "github.com/shini4i/argo-compare/internal/helpers"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	// Most of the time, we want to avoid huge output containing helm labels update only,
	// but we still want to be able to see the diff if needed
	if !preserveHelmLabels {
		c.findAndStripHelmLabels()
	}

	wg := new(sync.WaitGroup)
	wg.Add(2)

	go func() {
		if srcFiles, err := h.FindYamlFiles(filepath.Join(tmpDir, "templates/src")); err == nil {
			c.srcFiles = c.processFiles(srcFiles, "src")
		} else {
			log.Fatal(err)
		}
		wg.Done()
	}()

	go func() {
		if dstFiles, err := h.FindYamlFiles(filepath.Join(tmpDir, "templates/dst")); err == nil {
			c.dstFiles = c.processFiles(dstFiles, "dst")
		} else {
			// we are no longer failing here, because we need to support the case where the destination
			// branch does not have the Application yet
			log.Debugf("Error while finding files in %s: %s", filepath.Join(tmpDir, "templates/dst"), err)
		}
		wg.Done()
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

func (c *Compare) findNewOrRemovedFiles() {
	var newFiles []File
	var removedFiles []File

	for _, srcFile := range c.srcFiles {
		var found bool
		for _, dstFile := range c.dstFiles {
			if srcFile.Name == dstFile.Name {
				found = true
			}
		}
		if !found {
			newFiles = append(newFiles, srcFile)
		}
	}

	for _, dstFile := range c.dstFiles {
		var found bool
		for _, srcFile := range c.srcFiles {
			if dstFile.Name == srcFile.Name {
				found = true
			}
		}
		if !found {
			removedFiles = append(removedFiles, dstFile)
		}
	}

	c.addedFiles = newFiles
	c.removedFiles = removedFiles
}

func (c *Compare) printFilesStatus() {
	if len(c.addedFiles) == 0 && len(c.removedFiles) == 0 && len(c.diffFiles) == 0 {
		log.Info("No diff was found in rendered manifests!")
		return
	}

	if len(c.addedFiles) > 0 {
		log.Infof("The following %d file/files would be added:", len(c.addedFiles))
		for _, addedFile := range c.addedFiles {
			c.processManifest(addedFile, "added")
		}
	}

	if len(c.removedFiles) > 0 {
		log.Infof("The following %d file/files would be removed:", len(c.removedFiles))
		for _, removedFile := range c.removedFiles {
			c.processManifest(removedFile, "removed")
		}
	}

	if len(c.diffFiles) > 0 {
		log.Infof("The following %d file/files would be changed:", len(c.diffFiles))
		for _, diffFile := range c.diffFiles {
			c.processManifest(diffFile, "changed")
		}

	}
}

func (c *Compare) processManifest(file File, fileType string) {
	log.Infof(currentFilePrintPattern, file.Name)

	switch fileType {
	case "added":
		if printAddedManifests {
			color.Green(string(
				h.ReadFile(fmt.Sprintf(srcPathPattern, tmpDir, file.Name))),
			)
		}
	case "removed":
		if printRemovedManifests {
			color.Red(string(
				h.ReadFile(fmt.Sprintf(dstPathPattern, tmpDir, file.Name))),
			)
		}
	case "changed":
		c.printDiffFile(file)
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

		log.Debugf("Using custom diff command: %s", cyan(command))

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
