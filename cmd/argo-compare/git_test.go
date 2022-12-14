package main

import (
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"testing"

	h "github.com/shini4i/argo-compare/internal/helpers"
)

const (
	appFile = "test/data/test.yaml"
)

var (
	git                = GitRepo{}
	gitChangedFiles    = fmt.Sprintf("go.mod\n%s\n", appFile)
	changedFileContent = h.ReadFile("../../" + appFile)
)

func TestChangedFiles(t *testing.T) {
	stdout, _ := git.getChangedFiles(fakeChangedFile)

	if !h.Contains(stdout, appFile) {
		t.Errorf("test.yaml should be in the list")
	}
}

func TestGetChangedFileContent(t *testing.T) {
	content, _ := git.getChangedFileContent("main", appFile, fakeFileContent)

	app := Application{File: appFile}
	app.parse()

	if !reflect.DeepEqual(content, app.App) {
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
