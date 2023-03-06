package main

import (
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

	expectedAddedFiles := []File{{Name: file4}}
	expectedRemovedFiles := []File{{Name: file2}}
	expectedDiffFiles := []File{{Name: file1, Sha: "5678"}}

	c.generateFilesStatus()

	if !reflect.DeepEqual(c.addedFiles, expectedAddedFiles) {
		t.Errorf("generateFilesStatus() did not generate expected addedFiles")
	}

	if !reflect.DeepEqual(c.removedFiles, expectedRemovedFiles) {
		t.Errorf("generateFilesStatus() did not generate expected removedFiles")
	}

	if !reflect.DeepEqual(c.diffFiles, expectedDiffFiles) {
		t.Errorf("generateFilesStatus() did not generate expected diffFiles")
	}
}
