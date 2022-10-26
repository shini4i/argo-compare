package main

import (
	"fmt"
	"github.com/romana/rlog"
	m "github.com/shini4i/argo-compare/internal/models"
	"os"
	"os/exec"
)

type execContext = func(name string, arg ...string) *exec.Cmd

func processFiles(fileName string, fileType string, application m.Application) {
	rlog.Debugf("Processing %s changed files", fileType)

	app := Application{File: fileName, Type: fileType, App: application}
	if fileType == "src" {
		app.parse()
	}

	app.writeValuesYaml()
	app.collectHelmChart()
	app.extractChart()
	app.renderTemplate()
}

func compareFiles() {
	comparer := Compare{}
	comparer.findFiles()
	comparer.printCompareResults()
}

func main() {
	repo := GitRepo{}

	changedFiles := repo.getChangedFiles(exec.Command)

	if len(changedFiles) == 0 {
		fmt.Println("No changed Application files found. Exiting...")
		os.Exit(0)
	}

	for _, file := range changedFiles {
		fmt.Println("Processing changed application: ", file)
		fmt.Println()

		if _, err := os.Stat("tmp/"); os.IsNotExist(err) {
			err := os.Mkdir("tmp/", 0755)
			if err != nil {
				rlog.Criticalf(err.Error())
			}
		}

		processFiles(file, "src", m.Application{})
		app := repo.getChangedFileContent("main", file, exec.Command)
		processFiles(file, "dst", app)
		compareFiles()

		err := os.RemoveAll("tmp/")
		if err != nil {
			rlog.Criticalf(err.Error())
		}
	}
}
