package main

import (
	"bytes"
	"fmt"
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"os"
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
	expectedRemovedFiles := []File{{Name: file2, Sha: "90122"}}
	expectedDiffFiles := []File{{Name: file1, Sha: "1234"}}

	c.generateFilesStatus()

	assert.Equal(t, expectedAddedFiles, c.addedFiles)
	assert.Equal(t, expectedRemovedFiles, c.removedFiles)
	assert.Equal(t, expectedDiffFiles, c.diffFiles)
}

func TestFindAndStripHelmLabels(t *testing.T) {
	// Prepare test data
	testFile := "../../testdata/dynamic/deployment.yaml"
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
	if err := os.Chdir("../../testdata/dynamic"); err != nil {
		t.Fatalf("Failed to change directory: %s", err)
	}

	// Create an instance of Compare
	c := &Compare{}

	// Call the method to find and strip Helm labels
	c.findAndStripHelmLabels()

	// Return to the original directory
	if err := os.Chdir("../../cmd/argo-compare"); err != nil {
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

func TestProcessFiles(t *testing.T) {
	compare := &Compare{
		CmdRunner: &utils.RealCmdRunner{},
	}

	files := []string{
		"../../testdata/test.yaml",
		"../../testdata/test-values.yaml",
	}

	expectedFiles := []File{
		{
			Name: "../../testdata/test.yaml",
			Sha:  "e263e4264f5570000b3666d6d07749fb67d4b82a6a1e1c1736503adcb7942e5b",
		},
		{
			Name: "../../testdata/test-values.yaml",
			Sha:  "c22e6d877e8c49693306eb2d16affaa3a318fe602f36b6e733428e9c16ebfa32",
		},
	}

	foundFiles := compare.processFiles(files, "src")

	assert.Equal(t, expectedFiles, foundFiles)
}

func TestPrintFilesStatus(t *testing.T) {
	// Test case 1: No added, removed or changed files
	var buf bytes.Buffer
	backend := logging.NewLogBackend(&buf, "", 0)
	logging.SetBackend(backend)

	c := &Compare{
		CmdRunner: &utils.RealCmdRunner{},
	}
	c.printFilesStatus()

	logs := buf.String()
	assert.Contains(t, logs, "No diff was found in rendered manifests!")

	// Test case 2: Found added and removed files, but no changed files
	backend = logging.NewLogBackend(&buf, "", 0)

	logging.SetBackend(backend)

	c = &Compare{
		addedFiles:   []File{{Name: "file1", Sha: "123"}},
		removedFiles: []File{{Name: "file2", Sha: "456"}, {Name: "file3", Sha: "789"}},
		diffFiles:    []File{},
	}

	c.printFilesStatus()

	logs = buf.String()
	assert.Contains(t, logs, "The following 1 file would be added:")
	assert.Contains(t, logs, "The following 2 files would be removed:")
	assert.NotContains(t, logs, "The following 1 file would be changed:")
}

// TestRunCustomDiffCommand tests the runCustomDiffCommand function.
//
// This test primarily validates the behavior of the function, ensuring
// that it calls the Run method of CmdRunner with the correct parameters.
//
// It uses a mock CmdRunner to provide controlled responses and to record
// the calls made to it. If the runCustomDiffCommand function doesn't make
// the expected call to the Run method of the CmdRunner, this test will fail,
// indicating that the function isn't behaving as we expect.
//
// This test isn't concerned with the actual output of the runCustomDiffCommand function,
// because the function doesn't return any result; it only logs the output.
//
// So, the primary purpose of this test is to verify that the function
// behaves correctly in terms of its interaction with the CmdRunner's Run method.
func TestRunCustomDiffCommand(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	compare := &Compare{
		CmdRunner: mockCmdRunner,
	}

	diffFile := File{Name: "test"}

	command := fmt.Sprintf(diffCommand,
		fmt.Sprintf(dstPathPattern, tmpDir, diffFile.Name),
		fmt.Sprintf(srcPathPattern, tmpDir, diffFile.Name),
	)

	mockCmdRunner.EXPECT().Run("sh", "-c", command).Return("stdout", "stderr", nil)

	compare.runCustomDiffCommand(diffFile)
}
