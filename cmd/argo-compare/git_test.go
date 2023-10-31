package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

const (
	appFile = "testdata/test.yaml"
)

func init() {
	// We don't want to see any logs in tests
	loggingInit(logging.CRITICAL)
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

	_, err = repo.getChangedFiles(utils.OsFileReader{})
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
	appFileContent := string(ReadFile("../../" + appFile))

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

	// Test case 4: temporary file creation fails
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "show", gomock.Any()).Return(appFileContent, "", nil)
	repo = &GitRepo{FsType: afero.NewReadOnlyFs(afero.NewMemMapFs()), CmdRunner: mockCmdRunner}
	content, err = repo.getChangedFileContent("main", appFile)

	assert.Equal(t, content, models.Application{}, "content should be empty")
	assert.ErrorIsf(t, err, os.ErrPermission, "expected os.ErrPermission, got %v", err)
}

func TestCheckIfApp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create the mocks
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	isApp, err := checkIfApp(mockCmdRunner, utils.OsFileReader{}, appFile)

	assert.True(t, isApp, "expected true, got false")
	assert.NoError(t, err, "expected no error, got %v", err)
}

func TestGetGitRepoRoot(t *testing.T) {
	// Test case 1: Check if the git repo root is found
	repoRoot, err := GetGitRepoRoot(&utils.RealCmdRunner{})
	if err != nil {
		t.Fatalf("error finding git repo root: %v", err)
	}
	assert.NotEmptyf(t, repoRoot, "expected repo root to be non-empty, but got [%s]", repoRoot)

	// Test case 2: Check if the git repo root could not be found
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	mockCmdRunner.EXPECT().Run("git", "rev-parse", "--show-toplevel").Return("", "", errors.New("git not found"))

	repoRoot, err = GetGitRepoRoot(mockCmdRunner)
	assert.Emptyf(t, repoRoot, "expected repo root to be empty, but got [%s]", repoRoot)
	assert.Errorf(t, err, "expected error to be returned, but got nil")
}

func TestReadFile(t *testing.T) {
	// Set up test environment
	repoRoot, err := GetGitRepoRoot(&utils.RealCmdRunner{})
	if err != nil {
		t.Fatalf("error finding git repo root: %v", err)
	}

	testFile := filepath.Join(repoRoot, "testdata/test.yaml")
	expectedContents := "apiVersion: argoproj.io/v1alpha1"

	// Test case 1: Check if a file is read successfully
	actualContents := ReadFile(testFile)
	if !strings.Contains(string(actualContents), expectedContents) {
		t.Errorf("expected file contents to contain [%s], but got [%s]", expectedContents, string(actualContents))
	}

	// Test case 2: Check if a missing file is handled properly
	missingFile := filepath.Join(repoRoot, "testdata/missing.yaml")
	actualContents = ReadFile(missingFile)
	assert.Nilf(t, actualContents, "expected file contents to be nil, but got [%s]", string(actualContents))
}
