package main

import (
	"fmt"
	"github.com/romana/rlog"
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"os/exec"

	h "github.com/shini4i/argo-compare/internal/helpers"
	m "github.com/shini4i/argo-compare/internal/models"
)

type Application struct {
	File          string
	Type          string // src or dst version
	App           m.Application
	chartLocation string
}

func (a *Application) parse() {
	app := m.Application{}

	if debug {
		fmt.Printf("Parsing %s...\n", a.File)
	}

	yamlFile := h.ReadFile(a.File)

	err := yaml.Unmarshal(yamlFile, &app)
	if err != nil {
		panic(err)
	}

	a.App = app
}

func (a *Application) writeValuesYaml() {
	yamlFile, err := os.Create(fmt.Sprintf("tmp/values-%s.yaml", a.Type))
	if err != nil {
		panic(err)
	}

	_, err = yamlFile.WriteString(a.App.Spec.Source.Helm.Values)
	if err != nil {
		panic(err)
	}
}

func (a *Application) collectHelmChart() {
	a.chartLocation = fmt.Sprintf("%s/%s", cacheDir, a.App.Spec.Source.RepoURL)

	if err := os.MkdirAll(a.chartLocation, os.ModePerm); err != nil {
		fmt.Println(err)
	}

	fmt.Println(cacheDir)

	if _, err := os.Stat(fmt.Sprintf("%s/%s-%s.tgz", a.chartLocation, a.App.Spec.Source.Chart, a.App.Spec.Source.TargetRevision)); os.IsNotExist(err) {
		if debug {
			fmt.Printf("Downloading version %s of %s chart...\n",
				a.App.Spec.Source.TargetRevision,
				a.App.Spec.Source.Chart)
		}

		cmd := exec.Command(
			"helm",
			"pull",
			"--destination", a.chartLocation,
			"--repo", a.App.Spec.Source.RepoURL,
			a.App.Spec.Source.Chart,
			"--version", a.App.Spec.Source.TargetRevision)

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		err := cmd.Run()

		if err != nil {
			rlog.Critical(err)
		}

	} else {
		if debug {
			fmt.Printf("Version %s of %s chart already downloaded...\n",
				a.App.Spec.Source.TargetRevision,
				a.App.Spec.Source.Chart)
		}
	}
}

func (a *Application) extractChart() {
	// We have a separate function for this and not using helm to extract the content of the chart
	// because we don't want to re-download the chart if the TargetRevision is the same
	if debug {
		fmt.Printf("Extracting %s chart to tmp/charts/%s...\n", a.App.Spec.Source.Chart, a.Type)
	}

	path := fmt.Sprintf("tmp/charts/%s/%s", a.Type, a.App.Spec.Source.Chart)
	if err := os.MkdirAll(path, os.ModePerm); err != nil {
		rlog.Critical(err)
	}

	cmd := exec.Command(
		"tar",
		"xf",
		fmt.Sprintf("%s/%s-%s.tgz", a.chartLocation, a.App.Spec.Source.Chart, a.App.Spec.Source.TargetRevision),
		"-C", fmt.Sprintf("tmp/charts/%s", a.Type),
	)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()

	if err != nil {
		log.Fatal(err)
	}
}

func (a *Application) renderTemplate() {
	if debug {
		fmt.Printf("Rendering %s template...\n", a.App.Spec.Source.Chart)
	}

	cmd := exec.Command(
		"helm",
		"template",
		fmt.Sprintf("tmp/charts/%s/%s", a.Type, a.App.Spec.Source.Chart),
		"--output-dir", fmt.Sprintf("tmp/templates/%s", a.Type),
		"--values", fmt.Sprintf("tmp/charts/%s/%s/values.yaml", a.Type, a.App.Spec.Source.Chart),
		"--values", fmt.Sprintf("tmp/values-%s.yaml", a.Type),
	)

	cmd.Stderr = os.Stderr

	err := cmd.Run()

	if err != nil {
		rlog.Critical(err)
	}
}
