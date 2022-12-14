package main

import (
	"crypto/sha256"
	"fmt"
	"github.com/mattn/go-zglob"
	"github.com/op/go-logging"
	h "github.com/shini4i/argo-compare/internal/helpers"
	"hash"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

type File struct {
	Name string
	Sha  hash.Hash
}

type Compare struct {
	srcFiles     []File
	dstFiles     []File
	diffFiles    []File
	addedFiles   []File
	removedFiles []File
}

func (c *Compare) findFiles() {
	srcFiles, err := zglob.Glob(fmt.Sprintf("%s/templates/src/**/*.yaml", tmpDir))
	if err != nil {
		panic(err)
	}
	c.srcFiles = c.processFiles(srcFiles)

	dstFiles, err := zglob.Glob(fmt.Sprintf("%s/templates/dst/**/*.yaml", tmpDir))
	if err != nil {
		panic(err)
	}
	c.dstFiles = c.processFiles(dstFiles)

	if !reflect.DeepEqual(c.srcFiles, c.dstFiles) {
		c.compareFiles()
	}

	c.findNewOrRemovedFiles()
}

func (c *Compare) processFiles(files []string) []File {
	var strippedFiles []File
	var file File

	// Most of the time, we want to avoid huge output containing helm labels update only
	// but we still want to be able to see the diff if needed
	if !preserveHelmLabels {
		c.findAndStripHelmLabels()
	}

	// TODO: Make this less ugly
	for _, srcFile := range files {
		s := strings.Split(srcFile, "/")
		var count int

		for _, v := range s {
			count += len(v)
			if v == "dst" || v == "src" {
				break
			}
		}

		count += 5 // add 5 for the length of /dst/ and /src/

		file = File{Name: srcFile[count:], Sha: getFileSha(srcFile)}
		strippedFiles = append(strippedFiles, file)
	}
	return strippedFiles
}

func getFileSha(file string) hash.Hash {
	// We are using SHA as a way to detect if two files are identical
	f, err := os.Open(file)
	if err != nil {
		log.Fatal(err.Error())
	}

	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			panic(err)
		}
	}(f)

	fileHash := sha256.New()
	if _, err := io.Copy(fileHash, f); err != nil {
		log.Fatal(err.Error())
	}

	return fileHash
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
		log.Info("No diff in rendered manifests found!")
		return
	}

	if len(c.addedFiles) > 0 {
		log.Infof("The following %d file/files would be added:", len(c.addedFiles))
		for _, addedFile := range c.addedFiles {
			log.Infof("??? %s", addedFile.Name)
		}
	}

	if len(c.removedFiles) > 0 {
		log.Infof("The following %d file/files would be removed:", len(c.removedFiles))
		for _, removedFile := range c.removedFiles {
			log.Infof("??? %s", removedFile.Name)
		}
	}

	if len(c.diffFiles) > 0 {
		log.Infof("The following %d file/files would be changed:", len(c.diffFiles))
		for _, diffFile := range c.diffFiles {
			log.Infof("??? %s", diffFile.Name)
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
