package main

import (
	"bytes"
	"github.com/op/go-logging"
	"github.com/r3labs/diff/v3"
	"reflect"
	"testing"
)

func TestHandleCreate(t *testing.T) {
	c := Compare{
		srcFiles: []File{},
		dstFiles: []File{{Name: "file1.txt", Sha: "1234"}},
	}

	filesStatus, err := diff.Diff(c.srcFiles, c.dstFiles)
	if err != nil {
		log.Fatal(err)
	}

	c.handleCreate(filesStatus[0])

	// Check that the created file was added to the `addedFiles` slice
	expectedAddedFiles := []File{{Name: "file1.txt"}}

	if !reflect.DeepEqual(c.addedFiles, expectedAddedFiles) {
		t.Errorf("handleCreate did not add created file to addedFiles slice")
	}
}

func TestHandleDelete(t *testing.T) {
	c := Compare{
		srcFiles: []File{{Name: "file1.txt", Sha: "1234"}},
		dstFiles: []File{},
	}

	filesStatus, err := diff.Diff(c.srcFiles, c.dstFiles)
	if err != nil {
		log.Fatal(err)
	}

	c.handleDelete(filesStatus[0])

	// Check that the deleted file was added to the `removedFiles` slice
	expectedRemovedFiles := []File{{Name: "file1.txt"}}

	if !reflect.DeepEqual(c.removedFiles, expectedRemovedFiles) {
		t.Errorf("handleDelete did not add deleted file to removedFiles slice")
	}
}

func TestHandleUpdate(t *testing.T) {
	c := Compare{
		srcFiles: []File{{Name: "file1.txt", Sha: "1234"}},
		dstFiles: []File{{Name: "file1.txt", Sha: "5678"}},
	}

	filesStatus, err := diff.Diff(c.dstFiles, c.srcFiles)
	if err != nil {
		log.Fatal(err)
	}

	c.handleUpdate(filesStatus[0])

	// Check that the updated file was added to the `diffFiles` slice
	expectedDiffFiles := []File{{Name: "file1.txt", Sha: "5678"}}

	if !reflect.DeepEqual(c.diffFiles, expectedDiffFiles) {
		t.Errorf("handleUpdate did not add updated file to diffFiles slice")
	}
}

func TestPrintFilesStatus(t *testing.T) {
	// Capture log output
	var logOutput bytes.Buffer
	logBackend := logging.NewLogBackend(&logOutput, "", 0)
	logging.SetBackend(logBackend)

	// Create Compare object
	c := Compare{
		addedFiles:   []File{{Name: "file1.txt"}},
		removedFiles: []File{{Name: "file2.txt"}, {Name: "file4.txt"}},
		diffFiles:    []File{{Name: "file3.txt"}},
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
