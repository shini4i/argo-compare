package main

import (
	"github.com/op/go-logging"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	h "github.com/shini4i/argo-compare/internal/helpers"
)

const (
	appFile = "testdata/test.yaml"
)

func init() {
	// We don't want to see any logs in tests
	loggingInit(logging.CRITICAL)
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

	// Setup the expectation
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "show", gomock.Any()).Return(appFileContent, "", nil)

	repo := &GitRepo{CmdRunner: mockCmdRunner}

	content, _ := repo.getChangedFileContent("main", appFile)

	target := Target{CmdRunner: mockCmdRunner, File: appFile}
	if err := target.parse(); err != nil {
		t.Errorf("test.yaml should be parsed")
	}

	assert.Equal(t, content, target.App, "content should be equal to app.App")
}

func TestCheckIfApp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create the mocks
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	isApp, err := checkIfApp(mockCmdRunner, appFile)
	if !isApp || err != nil {
		t.Errorf("test.yaml should be detected as app")
	}
}
