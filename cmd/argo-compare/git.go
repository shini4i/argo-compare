package main

import (
	"bytes"
	"github.com/romana/rlog"
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

func (g *GitRepo) getChangedFileContent(targetBranch string, targetFile string, cmdContext execContext) m.Application {
	rlog.Debugf("Getting content of %s from %s", targetFile, targetBranch)

	cmd := cmdContext("git", "--no-pager", "show", targetBranch+":"+targetFile)

	var out bytes.Buffer

	cmd.Stdout = &out
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		rlog.Criticalf(err.Error())
	}

	// writing the content to a temporary file to be able to pass it to the parser
	tmpFile, err := os.CreateTemp("/tmp", "compare-*.yaml")
	if err != nil {
		rlog.Criticalf(err.Error())
	}

	_, err = tmpFile.WriteString(out.String())
	if err != nil {
		rlog.Criticalf(err.Error())
	}

	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			rlog.Criticalf(err.Error())
		}
	}(tmpFile.Name())

	app := Application{File: tmpFile.Name()}
	app.parse()

	return app.App
}

func checkIfApp(file string) bool {
	rlog.Debugf("Checking if %s is an app", file)

	app := Application{File: file}
	app.parse()

	if app.App.Kind == "Application" {
		return true
	}
	return false
}
