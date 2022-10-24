package main

import (
	"bytes"
	"fmt"
	"github.com/romana/rlog"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"

	m "github.com/shini4i/argo-compare/internal/models"
)

type GitRepo struct {
	changedFiles []string
}

func (g *GitRepo) getChangedFiles(cmdContext execContext) []string {
	rlog.Println("Getting changed files")
	cmd := cmdContext("git", "--no-pager", "diff", "--name-only", "main")

	var out bytes.Buffer

	cmd.Stdout = &out
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		rlog.Criticalf(err.Error())
	}

	for _, file := range strings.Split(out.String(), "\n") {
		if filepath.Ext(file) == ".yaml" && checkIfApp(file) {
			g.changedFiles = append(g.changedFiles, file)
		}
	}

	rlog.Debugf("Changed files: %v", g.changedFiles)

	return g.changedFiles
}

func (g *GitRepo) getChangedFileContent(targetBranch string, targetFile string, cmdContext execContext) string {
	rlog.Debugf("Getting content of %s from %s", targetFile, targetBranch)

	cmd := cmdContext(fmt.Sprintf("git show %s:%s", targetBranch, targetFile))

	var out bytes.Buffer

	cmd.Stdout = &out
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		rlog.Criticalf(err.Error())
	}

	return out.String()
}

func checkIfApp(file string) bool {
	app := m.Application{}
	yamlFile, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}

	err = yaml.Unmarshal(yamlFile, &app)
	if err != nil {
		return false
	}

	if app.Kind == "Application" {
		return true
	}
	return false
}
