package main

import (
	"crypto/sha256"
	"github.com/mattn/go-zglob"
	"github.com/romana/rlog"
	h "github.com/shini4i/argo-compare/internal/helpers"
	"hash"
	"io"
	"os"
	"reflect"

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
	srcFiles, err := zglob.Glob("tmp/templates/src/**/*.yaml")
	if err != nil {
		panic(err)
	}
	c.srcFiles = c.processFiles(srcFiles)

	dstFiles, err := zglob.Glob("tmp/templates/dst/**/*.yaml")
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

	for _, srcFile := range files {
		file = File{Name: srcFile[18:], Sha: getFileSha(srcFile)}
		strippedFiles = append(strippedFiles, file)
	}
	return strippedFiles
}

func getFileSha(file string) hash.Hash {
	// We are using SHA as a way to detect if two files are identical
	f, err := os.Open(file)
	if err != nil {
		rlog.Criticalf(err.Error())
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			panic(err)
		}
	}(f)

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		rlog.Criticalf(err.Error())
	}

	return h
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
		rlog.Println("File: " + diffFile.Name + " is different")

		diff := diffmatchpatch.New()

		srcFile := string(h.ReadFile("tmp/templates/src/" + diffFile.Name))
		dstFile := string(h.ReadFile("tmp/templates/dst/" + diffFile.Name))

		diffs := diff.DiffMain(srcFile, dstFile, false)

		rlog.Println(diff.DiffPrettyText(diffs))
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
	if len(c.addedFiles) > 0 {
		rlog.Debugf("New files:")
		for _, addedFile := range c.addedFiles {
			rlog.Debugf(addedFile.Name)
		}
	}

	if len(c.removedFiles) > 0 {
		rlog.Debugf("Removed files:")
		for _, removedFile := range c.removedFiles {
			rlog.Debugf(removedFile.Name)
		}
	}

	if len(c.diffFiles) > 0 {
		rlog.Debugf("Files with differences:")
		for _, diffFile := range c.diffFiles {
			rlog.Debugf(diffFile.Name)
		}
		c.printDiffFiles()
	}
}
