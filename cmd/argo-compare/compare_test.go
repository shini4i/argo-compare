package main

import (
	"fmt"
	"reflect"
	"testing"
)

const (
	file1 = "file1.txt"
	file2 = "file2.txt"
	file3 = "file3.txt"
	file4 = "file4.txt"
)

func TestGenerateFilesStatus(t *testing.T) {
	srcFiles := []File{
		{Name: file1, Sha: "1234"},
		{Name: file3, Sha: "3456"},
		{Name: file4, Sha: "7890"},
	}

	dstFiles := []File{
		{Name: file1, Sha: "5678"},
		{Name: file2, Sha: "9012"},
		{Name: file3, Sha: "3456"},
	}

	c := Compare{
		srcFiles: srcFiles,
		dstFiles: dstFiles,
	}

	expectedAddedFiles := []File{{Name: file4, Sha: "7890"}}
	expectedRemovedFiles := []File{{Name: file2, Sha: "9012"}}
	expectedDiffFiles := []File{{Name: file1, Sha: "1234"}}

	c.generateFilesStatus()

	if !reflect.DeepEqual(c.addedFiles, expectedAddedFiles) {
		fmt.Println(c.addedFiles)
		t.Errorf("generateFilesStatus() did not generate expected addedFiles")
	}

	if !reflect.DeepEqual(c.removedFiles, expectedRemovedFiles) {
		fmt.Println(c.removedFiles)
		t.Errorf("generateFilesStatus() did not generate expected removedFiles")
	}

	if !reflect.DeepEqual(c.diffFiles, expectedDiffFiles) {
		fmt.Println(c.diffFiles)
		t.Errorf("generateFilesStatus() did not generate expected diffFiles")
	}
}
