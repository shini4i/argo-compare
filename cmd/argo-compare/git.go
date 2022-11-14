package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	m "github.com/shini4i/argo-compare/internal/models"
)

type GitRepo struct {
	changedFiles []string
}

func (g *GitRepo) getChangedFiles(cmdContext execContext) ([]string, error) {
	cmd := cmdContext("git", "--no-pager", "diff", "--name-only", targetBranch)

	var out bytes.Buffer

	cmd.Stdout = &out
	if debug {
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Run(); err != nil {
		return []string{}, err
	}

	printDebug(fmt.Sprintf("===> Found the following changed files:\n%s", out.String()))

	for _, file := range strings.Split(out.String(), "\n") {
		if filepath.Ext(file) == ".yaml" && checkIfApp(file) {
			g.changedFiles = append(g.changedFiles, file)
		}
	}

	if len(g.changedFiles) > 0 {
		fmt.Println("===> Found the following changed Application files")
		for _, file := range g.changedFiles {
			fmt.Printf("- %s\n", file)
		}
	}

	return g.changedFiles, nil
}

func (g *GitRepo) getChangedFileContent(targetBranch string, targetFile string, cmdContext execContext) (m.Application, error) {
	printDebug(fmt.Sprintf("Getting content of %s from %s", targetFile, targetBranch))

	cmd := cmdContext("git", "--no-pager", "show", targetBranch+":"+targetFile)

	var out bytes.Buffer

	cmd.Stdout = &out
	if debug {
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Run(); err != nil {
		return m.Application{}, err
	}

	// writing the content to a temporary file to be able to pass it to the parser
	tmpFile, err := os.CreateTemp("/tmp", "compare-*.yaml")
	if err != nil {
		fmt.Println(err.Error())
	}

	_, err = tmpFile.WriteString(out.String())
	if err != nil {
		fmt.Println(err.Error())
	}

	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			fmt.Println(err.Error())
		}
	}(tmpFile.Name())

	app := Application{File: tmpFile.Name()}
	app.parse()

	return app.App, nil
}

func checkIfApp(file string) bool {
	printDebug(fmt.Sprintf("===> Checking if [%s] is an Application", file))

	app := Application{File: file}
	app.parse()

	if app.App.Kind == "Application" {
		return true
	}
	return false
}
