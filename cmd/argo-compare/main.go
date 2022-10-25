package main

import (
	"github.com/romana/rlog"
	m "github.com/shini4i/argo-compare/internal/models"
	"os/exec"
)

type execContext = func(name string, arg ...string) *exec.Cmd

func processSrcFiles(fileName string) {
	rlog.Info("Processing changed files")
	app := Application{File: fileName, Type: "src"}
	app.parse()
	app.writeValuesYaml()
	app.collectHelmChart()
	app.extractChart()
	app.renderTemplate()
}

func processDstFiles(fileName string, application m.Application) {
	rlog.Info("Processing destination files")
	app := Application{File: fileName, App: application, Type: "dst"}
	app.writeValuesYaml()
	app.collectHelmChart()
	app.extractChart()
	app.renderTemplate()
}

func main() {
	repo := GitRepo{}

	for _, file := range repo.getChangedFiles(exec.Command) {
		processSrcFiles(file)
		app := repo.getChangedFileContent("main", file, exec.Command)
		processDstFiles(file, app)
	}

	comparer := Compare{}
	comparer.findFiles()
	comparer.printCompareResults()
}
