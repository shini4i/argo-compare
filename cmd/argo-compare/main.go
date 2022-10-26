package main

import (
	"github.com/romana/rlog"
	m "github.com/shini4i/argo-compare/internal/models"
	"os"
	"os/exec"
)

type execContext = func(name string, arg ...string) *exec.Cmd

func processFiles(fileName string, fileType string, application m.Application) {
	rlog.Infof("Processing %s changed files", fileType)

	app := Application{File: fileName, Type: fileType, App: application}
	if fileType == "src" {
		app.parse()
	}
	
	app.writeValuesYaml()
	app.collectHelmChart()
	app.extractChart()
	app.renderTemplate()
}

func main() {
	if _, err := os.Stat("tmp/"); os.IsNotExist(err) {
		err := os.Mkdir("tmp/", 0755)
		if err != nil {
			rlog.Criticalf(err.Error())
		}
	}

	defer func() {
		err := os.RemoveAll("tmp/")
		if err != nil {
			rlog.Criticalf(err.Error())
		}
	}()

	repo := GitRepo{}

	for _, file := range repo.getChangedFiles(exec.Command) {
		processFiles(file, "src", m.Application{})
		app := repo.getChangedFileContent("main", file, exec.Command)
		processFiles(file, "dst", app)
	}

	comparer := Compare{}
	comparer.findFiles()
	comparer.printCompareResults()
}
