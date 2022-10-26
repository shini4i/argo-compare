package main

import (
	"crypto/sha256"
	"fmt"
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

	fileHash := sha256.New()
	if _, err := io.Copy(fileHash, f); err != nil {
		rlog.Criticalf(err.Error())
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
		diff := diffmatchpatch.New()

		srcFile := string(h.ReadFile("tmp/templates/src/" + diffFile.Name))
		dstFile := string(h.ReadFile("tmp/templates/dst/" + diffFile.Name))

		diffs := diff.DiffMain(dstFile, srcFile, false)

		fmt.Println(diff.DiffPrettyText(diffs))
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
		fmt.Println("The following files would be added:")
		for _, addedFile := range c.addedFiles {
			fmt.Println(" - " + addedFile.Name)
		}
		fmt.Println()
	}

	if len(c.removedFiles) > 0 {
		fmt.Println("The following files would be removed:")
		for _, removedFile := range c.removedFiles {
			fmt.Println(" - " + removedFile.Name)
		}
		fmt.Println()
	}

	if len(c.diffFiles) > 0 {
		fmt.Println("The following files would be changed:")
		for _, diffFile := range c.diffFiles {
			fmt.Println(" - " + diffFile.Name)
		}
		fmt.Println()
		c.printDiffFiles()
	}
}
