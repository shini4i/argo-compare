package main

import (
	"fmt"
	"github.com/golang/mock/gomock"
	"github.com/op/go-logging"
	"github.com/stretchr/testify/assert"
	"os"
	"os/exec"
	"reflect"
	"testing"

	mocks "github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	h "github.com/shini4i/argo-compare/internal/helpers"
)

const (
	appFile = "testdata/test.yaml"
)

var (
	git                = GitRepo{}
	gitChangedFiles    = fmt.Sprintf("go.mod\n%s\n", appFile)
	changedFileContent = h.ReadFile("../../" + appFile)
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
	content, _ := git.getChangedFileContent("main", appFile, fakeFileContent)

	target := Target{File: appFile}
	if err := target.parse(); err != nil {
		t.Errorf("test.yaml should be parsed")
	}

	if !reflect.DeepEqual(content, target.App) {
		t.Errorf("content should be equal to app.App")
	}
}

func TestChangedFilesSuccess(t *testing.T) {
	if os.Getenv("GO_TEST_PROCESS") != "1" {
		return
	}
	_, err := fmt.Fprintf(os.Stdout, gitChangedFiles)
	if err != nil {
		return
	}
	os.Exit(0)
}

func TestFileContentSuccess(t *testing.T) {
	if os.Getenv("GO_TEST_PROCESS") != "1" {
		return
	}
	_, err := fmt.Fprintf(os.Stdout, string(changedFileContent))
	if err != nil {
		return
	}
	os.Exit(0)
}

func TestCheckIfApp(t *testing.T) {
	isApp, err := checkIfApp(appFile)
	if !isApp || err != nil {
		t.Errorf("test.yaml should be detected as app")
	}
}

func fakeChangedFile(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestChangedFilesSuccess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_TEST_PROCESS=1"}
	return cmd
}

func fakeFileContent(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestFileContentSuccess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_TEST_PROCESS=1"}
	return cmd
}
