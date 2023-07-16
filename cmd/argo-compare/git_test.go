package main

import (
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/spf13/afero"
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
			return helpers.ReadFile("../../" + appFile)
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

func TestGetChangedFiles(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create the mocks
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	mockFileReader := mocks.NewMockFileReader(ctrl)

	// Test case 1: valid yaml file
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "diff", "--name-only", gomock.Any()).Return("testdata/test.yaml\nfile2", "", nil)

	repo := &GitRepo{FsType: afero.NewOsFs(), CmdRunner: mockCmdRunner}

	files, err := repo.getChangedFiles(utils.OsFileReader{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	assert.Len(t, files, 1, "expected 1 file")
	assert.Equal(t, "testdata/test.yaml", files[0])

	// Test case 2: failed to run git command
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "diff", "--name-only", gomock.Any()).Return("", "some meaningful error", os.ErrNotExist)

	repo = &GitRepo{FsType: afero.NewOsFs(), CmdRunner: mockCmdRunner}

	files, err = repo.getChangedFiles(utils.OsFileReader{})
	// We expect to get an error if git command fails
	assert.ErrorIsf(t, err, os.ErrNotExist, "expected os.ErrNotExist, got %v", err)

	// Test case 3: invalid yaml file
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "diff", "--name-only", gomock.Any()).Return("testdata/test.yaml\nfile2", "", nil)
	mockFileReader.EXPECT().ReadFile(gomock.Any()).DoAndReturn(func(path string) []byte {
		if strings.HasSuffix(path, appFile) {
			return []byte("test\n\tvery invalid yaml")
		}
		return nil
	})

	repo = &GitRepo{FsType: afero.NewOsFs(), CmdRunner: mockCmdRunner}
	files, err = repo.getChangedFiles(mockFileReader)

	// invalid file should not produce an error, but should be skipped and added to the list of invalid files
	assert.NoError(t, err, "expected no error, got %v", err)
	// we expect to get 1 file in repo.invalidFiles
	assert.Len(t, repo.invalidFiles, 1, "expected 1 file")
	// we expect to get 0 found files
	assert.Len(t, files, 0, "expected 0 file")
}

func TestGetChangedFileContent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create the mocks
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	appFileContent := string(helpers.ReadFile("../../" + appFile))

	// Test case 1: file exists and is Application
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "show", gomock.Any()).Return(appFileContent, "", nil)
	repo := &GitRepo{FsType: afero.NewOsFs(), CmdRunner: mockCmdRunner}
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

	// Test case 3: we got an unexpected error
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "show", gomock.Any()).Return("", "some meaningful error", os.ErrNotExist)
	repo = &GitRepo{FsType: afero.NewOsFs(), CmdRunner: mockCmdRunner}
	_, err = repo.getChangedFileContent("main", appFile)
	assert.ErrorIsf(t, err, os.ErrNotExist, "expected os.ErrNotExist, got %v", err)
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
