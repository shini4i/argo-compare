package main

import (
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	h "github.com/shini4i/argo-compare/internal/helpers"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	appFile = "testdata/test.yaml"
)

func init() {
	// We don't want to see any logs in tests
	loggingInit(logging.CRITICAL)
}

func TestCheckFile(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create the mocks
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	mockFileReader := mocks.NewMockFileReader(ctrl)

	// Test case 1: file exists and is Application
	mockFileReader.EXPECT().ReadFile(gomock.Any()).DoAndReturn(func(path string) []byte {
		if strings.HasSuffix(path, appFile) {
			return h.ReadFile("../../" + appFile)
		}
		return nil
	})
	isApp, err := checkFile(mockCmdRunner, mockFileReader, appFile)

	assert.True(t, isApp, "expected true, got false")
	assert.NoError(t, err, "expected no error, got %v", err)

	// Test case 2: file exists and is not Application
	mockFileReader.EXPECT().ReadFile(gomock.Any()).DoAndReturn(func(path string) []byte {
		if strings.HasSuffix(path, appFile) {
			return []byte("test")
		}
		return nil
	})
	isApp, err = checkFile(mockCmdRunner, mockFileReader, appFile)

	assert.False(t, isApp, "expected false, got true")
	assert.ErrorIsf(t, err, invalidFileError, "expected invalidFileError, got %v", err)
}

func TestChangedFiles(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create the mocks
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	// Setup the expectation
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "diff", "--name-only", gomock.Any()).Return("testdata/test.yaml\nfile2", "", nil)

	repo := &GitRepo{CmdRunner: mockCmdRunner}

	files, err := repo.getChangedFiles()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	assert.Len(t, files, 1, "expected 1 file")
	assert.Equal(t, "testdata/test.yaml", files[0])
}

func TestGetChangedFileContent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create the mocks
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	appFileContent := string(h.ReadFile("../../" + appFile))

	// Test case 1: file exists and is Application
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "show", gomock.Any()).Return(appFileContent, "", nil)
	repo := &GitRepo{CmdRunner: mockCmdRunner}
	content, _ := repo.getChangedFileContent("main", appFile)
	target := Target{CmdRunner: mockCmdRunner, FileReader: utils.OsFileReader{}, File: appFile}
	if err := target.parse(); err != nil {
		t.Errorf("test.yaml should be parsed")
	}
	assert.Equal(t, content, target.App, "content should be equal to app.App")

	// Test case 2: file does not exist in target branch, and we do not plan to print added/removed files
	osErr := &exec.ExitError{
		ProcessState: &os.ProcessState{},
	}
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "show", gomock.Any()).Return("", "exists on disk, but not in", osErr)
	repo = &GitRepo{CmdRunner: mockCmdRunner}
	_, err := repo.getChangedFileContent("main", appFile)
	assert.ErrorIsf(t, err, gitFileDoesNotExist, "expected gitFileDoesNotExist, got %v", err)
}

func TestCheckIfApp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create the mocks
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	isApp, err := checkIfApp(mockCmdRunner, utils.OsFileReader{}, appFile)
	if !isApp || err != nil {
		t.Errorf("test.yaml should be detected as app")
	}
}
