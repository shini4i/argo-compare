package main

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	h "github.com/shini4i/argo-compare/internal/helpers"
)

const (
	appFile = "../../test/data/app-src.yaml"
)

var (
	git             = GitRepo{}
	gitChangedFiles = fmt.Sprintf("go.mod\n%s\n", appFile)
)

func TestChangedFiles(t *testing.T) {
	stdout := git.getChangedFiles(fakeChangedFile)

	if !h.Contains(stdout, appFile) {
		t.Errorf("test.yaml should be in the list")
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

func TestCheckIfApp(t *testing.T) {
	if !checkIfApp(appFile) {
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
