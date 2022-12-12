package main

import (
	"crypto/sha256"
	"fmt"
	"github.com/mattn/go-zglob"
	h "github.com/shini4i/argo-compare/internal/helpers"
	"hash"
	"io"
	"os"
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

	// TODO: Make this less ugly
	for _, srcFile := range files {
		s := strings.Split(srcFile, "/")
		var count int

		for _, v := range s {
			count += len(v)
			if v == "dst" {
				break
			} else if v == "src" {
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
		fmt.Println(err.Error())
	}

	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			panic(err)
		}
	}(f)

	fileHash := sha256.New()
	if _, err := io.Copy(fileHash, f); err != nil {
		fmt.Println(err.Error())
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

		srcFile := string(h.ReadFile(tmpDir + "/templates/src/" + diffFile.Name))
		dstFile := string(h.ReadFile(tmpDir + "/templates/dst/" + diffFile.Name))

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
		fmt.Printf("The following %d file/files would be added:\n", len(c.addedFiles))
		for _, addedFile := range c.addedFiles {
			fmt.Printf("- %s\n", addedFile.Name)
		}
	}

	if len(c.removedFiles) > 0 {
		fmt.Printf("The following %d file/files would be removed:\n", len(c.removedFiles))
		for _, removedFile := range c.removedFiles {
			fmt.Printf("- %s\n", removedFile.Name)
		}
	}

	if len(c.diffFiles) > 0 {
		fmt.Printf("The following %d file/files would be changed:\n", len(c.diffFiles))
		for _, diffFile := range c.diffFiles {
			fmt.Printf("- %s\n", diffFile.Name)
		}
		c.printDiffFiles()
	}
}

func (c *Compare) findAndStripHelmAnnotations() {
	helmFiles, err := zglob.Glob(fmt.Sprintf("%s/templates/**/*.yaml", tmpDir))
	if err != nil {
		panic(err)
	}

	for _, helmFile := range helmFiles {
		h.StripHelmAnnotations(helmFile)
	}
}
