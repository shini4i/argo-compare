package main

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"os"
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

func TestFindAndStripHelmLabels(t *testing.T) {
	// Prepare test data
	testFile := "../../testdata/deployment.yaml"
	backupFile := testFile + ".bak"

	// Read the original file content
	originalData, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read the original file: %s", err)
	}

	// Create a backup of the original file
	if err := os.WriteFile(backupFile, originalData, 0644); err != nil {
		t.Fatalf("Failed to create a backup of the test file: %s", err)
	}
	defer func() {
		// Restore the original file
		err := os.Rename(backupFile, testFile)
		if err != nil {
			t.Fatalf("Failed to restore the test file: %s", err)
		}
	}()

	// Change directory to the testdata directory
	if err := os.Chdir("../../testdata"); err != nil {
		t.Fatalf("Failed to change directory: %s", err)
	}

	// Create an instance of Compare
	c := &Compare{}

	// Call the method to find and strip Helm labels
	c.findAndStripHelmLabels()

	// Return to the original directory
	if err := os.Chdir("../cmd/argo-compare"); err != nil {
		t.Fatalf("Failed to change directory: %s", err)
	}

	// Read the modified file
	modifiedData, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read the modified file: %s", err)
	}

	// Define the expected modified content
	expectedOutput := `# for testing purpose we need only limited fields
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app.kubernetes.io/instance: traefik-web
    app.kubernetes.io/name: traefik
    argocd.argoproj.io/instance: traefik
  name: traefik
  namespace: web
`

	// Compare the modified output with the expected output
	assert.Equal(t, expectedOutput, string(modifiedData))
}
