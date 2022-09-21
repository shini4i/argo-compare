package main

import (
	"bytes"
	"github.com/romana/rlog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type GitRepo struct {
	changedFiles []string
}

func (g *GitRepo) getChangedFiles() {
	rlog.Println("Getting changed files")
	cmd := exec.Command("git", "--no-pager", "diff", "--name-only", "main")

	var out bytes.Buffer

	cmd.Stdout = &out
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		rlog.Criticalf(err.Error())
	}

	for _, file := range strings.Split(out.String(), "\n") {
		if filepath.Ext(file) == ".yaml" {
			g.changedFiles = append(g.changedFiles, file)
		}
	}
}
