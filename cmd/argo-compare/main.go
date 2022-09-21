package main

import "os/exec"

type execContext = func(name string, arg ...string) *exec.Cmd

func main() {
	app := Application{File: "test/data/app-src.yaml", Type: "src"}
	app.parse()
	app.writeValuesYaml()
	app.collectHelmChart()
	app.extractChart()
	app.renderTemplate()

	app2 := Application{File: "test/data/app-dst.yaml", Type: "dst"}
	app2.parse()
	app2.writeValuesYaml()
	if app.App.Spec.Source.TargetRevision != app2.App.Spec.Source.TargetRevision {
		app2.collectHelmChart()
	}
	app2.extractChart()
	app2.renderTemplate()

	comparer := Compare{}
	comparer.findFiles()
	comparer.printCompareResults()

	git := GitRepo{}
	git.getChangedFiles(exec.Command)
}
