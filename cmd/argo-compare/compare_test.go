package main

import (
	"bytes"
	"github.com/op/go-logging"
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

func TestPrintFilesStatus(t *testing.T) {
	// Capture log output
	var logOutput bytes.Buffer
	logBackend := logging.NewLogBackend(&logOutput, "", 0)
	logging.SetBackend(logBackend)

	// Create Compare object
	c := Compare{
		addedFiles:   []File{{Name: file1}},
		removedFiles: []File{{Name: file2}, {Name: file4}},
		diffFiles:    []File{{Name: file3}},
	}

	c.printFilesStatus()

	expectedOutput := "The following 1 file/files would be added:\n▶ file1.txt\n" +
		"The following 2 file/files would be removed:\n▶ file2.txt\n▶ file4.txt\n" +
		"The following 1 file/files would be changed:\n▶ file3.txt\n\n"

	if logOutput.String() != expectedOutput {
		t.Errorf("printFilesStatus output is incorrect. Expected:\n%s\nGot:\n%s",
			expectedOutput, logOutput.String())
	}
}
