package main

import (
	"fmt"
	"github.com/codingsince1985/checksum"
	"github.com/mattn/go-zglob"
	"github.com/op/go-logging"
	h "github.com/shini4i/argo-compare/internal/helpers"
	"os"
	"os/exec"
	"reflect"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

type File struct {
	Name string
	Sha  string
}

type Compare struct {
	srcFiles     []File
	dstFiles     []File
	diffFiles    []File
	addedFiles   []File
	removedFiles []File
}

func (c *Compare) findFiles() {
	if srcFiles, err := zglob.Glob(fmt.Sprintf("%s/templates/src/**/*.yaml", tmpDir)); err != nil {
		log.Fatal(err)
	} else {
		c.srcFiles = c.processFiles(srcFiles, "src")
	}

	if dstFiles, err := zglob.Glob(fmt.Sprintf("%s/templates/dst/**/*.yaml", tmpDir)); err != nil {
		log.Fatal(err)
	} else {
		c.dstFiles = c.processFiles(dstFiles, "dst")
	}

	if !reflect.DeepEqual(c.srcFiles, c.dstFiles) {
		c.compareFiles()
	}

	c.findNewOrRemovedFiles()
}

func (c *Compare) processFiles(files []string, filesType string) []File {
	var processedFiles []File

	// Most of the time, we want to avoid huge output containing helm labels update only,
	// but we still want to be able to see the diff if needed
	if !preserveHelmLabels {
		c.findAndStripHelmLabels()
	}

	substring := fmt.Sprintf("/%s/", filesType)

	for _, file := range files {
		if sha256sum, err := checksum.SHA256sum(file); err != nil {
			log.Fatal(err)
		} else {
			processedFiles = append(processedFiles, File{Name: strings.Split(file, substring)[1], Sha: sha256sum})
		}
	}

	return processedFiles
}

func (c *Compare) compareFiles() {
	var diffFiles []File

	for _, srcFile := range c.srcFiles {
		for _, dstFile := range c.dstFiles {
			if srcFile.Name == dstFile.Name && !reflect.DeepEqual(srcFile.Sha, dstFile.Sha) {
				diffFiles = append(diffFiles, srcFile)
			}
		}
	}

	c.diffFiles = diffFiles
}

func (c *Compare) printDiffFiles() {
	for _, diffFile := range c.diffFiles {
		switch diffCommand {
		case "built-in":
			diff := diffmatchpatch.New()

			srcFile := string(h.ReadFile(tmpDir + "/templates/src/" + diffFile.Name))
			dstFile := string(h.ReadFile(tmpDir + "/templates/dst/" + diffFile.Name))

			diffs := diff.DiffMain(dstFile, srcFile, false)

			log.Info(diff.DiffPrettyText(diffs))
		default:
			command := fmt.Sprintf(
				diffCommand,
				tmpDir+"/templates/dst/"+diffFile.Name,
				tmpDir+"/templates/src/"+diffFile.Name,
			)

			log.Debugf("Using custom diff command: %s", command)

			cmd := exec.Command("bash", "-c", command)
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

func (c *Compare) printCompareResults() {
	if len(c.addedFiles) == 0 && len(c.removedFiles) == 0 && len(c.diffFiles) == 0 {
		log.Info("No diff was found in rendered manifests!")
		return
	}

	if len(c.addedFiles) > 0 {
		log.Infof("The following %d file/files would be added:", len(c.addedFiles))
		for _, addedFile := range c.addedFiles {
			log.Infof("▶ %s", addedFile.Name)
		}
	}

	if len(c.removedFiles) > 0 {
		log.Infof("The following %d file/files would be removed:", len(c.removedFiles))
		for _, removedFile := range c.removedFiles {
			log.Infof("▶ %s", removedFile.Name)
		}
	}

	if len(c.diffFiles) > 0 {
		log.Infof("The following %d file/files would be changed:", len(c.diffFiles))
		for _, diffFile := range c.diffFiles {
			log.Infof("▶ %s", diffFile.Name)
		}
		c.printDiffFiles()
	}
}

func (c *Compare) findAndStripHelmLabels() {
	helmFiles, err := zglob.Glob(fmt.Sprintf("%s/templates/**/*.yaml", tmpDir))
	if err != nil {
		panic(err)
	}

	for _, helmFile := range helmFiles {
		h.StripHelmLabels(helmFile)
	}
}
