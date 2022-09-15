package main

import (
	"crypto/sha256"
	"github.com/mattn/go-zglob"
	"hash"
	"io"
	"log"
	"os"
	"os/exec"
	"reflect"
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

	c.printDiffFiles()
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
		log.Fatal(err)
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			panic(err)
		}
	}(f)

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		log.Fatal(err)
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
		log.Println("File: " + diffFile.Name + " is different")

		cmd := exec.Command("diff", "tmp/templates/src/"+diffFile.Name, "tmp/templates/dst/"+diffFile.Name)

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		// We don't care about the error here, as diff exits 1 if there are differences
		_ = cmd.Run()
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
