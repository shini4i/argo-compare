package main

import (
	"fmt"
	"github.com/codingsince1985/checksum"
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

func (c *Compare) findFiles() {
	if srcFiles, err := h.FindYamlFiles(filepath.Join(tmpDir, "templates/src")); err == nil {
		c.srcFiles = c.processFiles(srcFiles, "src")
	} else {
		log.Fatal(err)
	}

	if dstFiles, err := h.FindYamlFiles(filepath.Join(tmpDir, "templates/dst")); err == nil {
		c.dstFiles = c.processFiles(dstFiles, "dst")
	} else {
		log.Fatal(err)
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

func (c *Compare) generateFilesStatus() {
	filesStatus, err := diff.Diff(c.dstFiles, c.srcFiles)
	if err != nil {
		log.Fatal(err)
	}

	for _, fileStatus := range filesStatus {
		switch fileStatus.Type {
		case "create":
			if fileStatus.Path[1] == "name" {
				c.addedFiles = append(c.addedFiles, File{Name: fileStatus.To.(string)})
			}
		case "delete":
			if fileStatus.Path[1] == "name" {
				c.removedFiles = append(c.removedFiles, File{Name: fileStatus.From.(string)})
			}
		case "update":
			for _, diffFile := range c.dstFiles {
				if diffFile.Sha == fileStatus.From.(string) {
					c.diffFiles = append(c.diffFiles, diffFile)
				}
			}
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

func (c *Compare) printDiffFiles() {
	for _, diffFile := range c.diffFiles {
		switch diffCommand {
		case "built-in":
			differ := diffmatchpatch.New()

			srcFile := string(h.ReadFile(fmt.Sprintf("%s/templates/src/%s", tmpDir, diffFile.Name)))
			dstFile := string(h.ReadFile(fmt.Sprintf("%s/templates/dst/%s", tmpDir, diffFile.Name)))

			diffs := differ.DiffMain(dstFile, srcFile, false)

			log.Info(differ.DiffPrettyText(diffs))
		default:
			command := fmt.Sprintf(diffCommand,
				fmt.Sprintf("%s/templates/dst/%s", tmpDir, diffFile.Name),
				fmt.Sprintf("%s/templates/src/%s", tmpDir, diffFile.Name),
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
