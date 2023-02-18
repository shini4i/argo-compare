package main

import (
	"github.com/r3labs/diff/v3"
	"reflect"
	"testing"
)

func TestHandleUpdate(t *testing.T) {
	c := Compare{
		srcFiles: []File{{Name: "file1.txt", Sha: "1234"}},
		dstFiles: []File{{Name: "file1.txt", Sha: "5678"}},
	}

	filesStatus, err := diff.Diff(c.dstFiles, c.srcFiles)
	if err != nil {
		log.Fatal(err)
	}

	// Call the handleUpdate function
	c.handleUpdate(filesStatus[0])

	// Check that the updated file was added to the `diffFiles` slice
	expectedDiffFiles := []File{{Name: "file1.txt", Sha: "5678"}}

	if !reflect.DeepEqual(c.diffFiles, expectedDiffFiles) {
		t.Errorf("handleUpdate did not add updated file to diffFiles slice")
	}
}
