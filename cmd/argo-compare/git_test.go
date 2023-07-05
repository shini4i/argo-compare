package main

import (
	"github.com/golang/mock/gomock"
	"github.com/op/go-logging"
	"github.com/stretchr/testify/assert"
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
	mockOsFs := mocks.NewMockOsFs(ctrl)

	// Setup the expectation
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "diff", "--name-only", gomock.Any()).Return("testdata/test.yaml\nfile2", "", nil)

	repo := &GitRepo{
		CmdRunner: mockCmdRunner,
		OsFs:      mockOsFs,
	}

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
	mockOsFs := mocks.NewMockOsFs(ctrl)

	appFileContent := string(h.ReadFile("../../" + appFile))

	// Setup the expectation
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "show", gomock.Any()).Return(appFileContent, "", nil)

	repo := &GitRepo{
		CmdRunner: mockCmdRunner,
		OsFs:      mockOsFs,
	}

	content, _ := repo.getChangedFileContent("main", appFile)

	target := Target{File: appFile}
	if err := target.parse(); err != nil {
		t.Errorf("test.yaml should be parsed")
	}

	assert.Equal(t, content, target.App, "content should be equal to app.App")
}

func TestCheckIfApp(t *testing.T) {
	isApp, err := checkIfApp(appFile)
	if !isApp || err != nil {
		t.Errorf("test.yaml should be detected as app")
	}
}
