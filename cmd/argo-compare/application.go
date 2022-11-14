package main

import (
	"errors"
	"fmt"
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

	printDebug(fmt.Sprintf("Parsing %s...", a.File))

	yamlFile := h.ReadFile(a.File)

	err := yaml.Unmarshal(yamlFile, &app)
	if err != nil {
		panic(err)
	}

	a.App = app
}

func (a *Application) writeValuesYaml() {
	yamlFile, err := os.Create(fmt.Sprintf("%s/values-%s.yaml", tmpDir, a.Type))
	if err != nil {
		panic(err)
	}

	_, err = yamlFile.WriteString(a.App.Spec.Source.Helm.Values)
	if err != nil {
		panic(err)
	}
}

func (a *Application) collectHelmChart() error {
	a.chartLocation = fmt.Sprintf("%s/%s", cacheDir, a.App.Spec.Source.RepoURL)

	if err := os.MkdirAll(a.chartLocation, os.ModePerm); err != nil {
		fmt.Println(err)
	}

	if _, err := os.Stat(fmt.Sprintf("%s/%s-%s.tgz", a.chartLocation, a.App.Spec.Source.Chart, a.App.Spec.Source.TargetRevision)); os.IsNotExist(err) {
		printDebug(fmt.Sprintf("Downloading version %s of %s chart...",
			a.App.Spec.Source.TargetRevision,
			a.App.Spec.Source.Chart))

		cmd := exec.Command(
			"helm",
			"pull",
			"--destination", a.chartLocation,
			"--repo", a.App.Spec.Source.RepoURL,
			a.App.Spec.Source.Chart,
			"--version", a.App.Spec.Source.TargetRevision)

		cmd.Stdout = os.Stdout
		if debug {
			cmd.Stderr = os.Stderr
		}

		if err := cmd.Run(); err != nil {
			return errors.New("error downloading chart")
		}
	} else {
		printDebug(fmt.Sprintf("Version %s of %s chart already downloaded...",
			a.App.Spec.Source.TargetRevision,
			a.App.Spec.Source.Chart))
	}

	return nil
}

func (a *Application) extractChart() {
	// We have a separate function for this and not using helm to extract the content of the chart
	// because we don't want to re-download the chart if the TargetRevision is the same
	printDebug(fmt.Sprintf("Extracting %s chart to %s/charts/%s...", a.App.Spec.Source.Chart, tmpDir, a.Type))

	path := fmt.Sprintf("%s/charts/%s/%s", tmpDir, a.Type, a.App.Spec.Source.Chart)
	if err := os.MkdirAll(path, os.ModePerm); err != nil {
		fmt.Println(err)
	}

	cmd := exec.Command(
		"tar",
		"xf",
		fmt.Sprintf("%s/%s-%s.tgz", a.chartLocation, a.App.Spec.Source.Chart, a.App.Spec.Source.TargetRevision),
		"-C", fmt.Sprintf("%s/charts/%s", tmpDir, a.Type),
	)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()

	if err != nil {
		log.Fatal(err)
	}
}

func (a *Application) renderTemplate() {
	printDebug(fmt.Sprintf("Rendering %s template...", a.App.Spec.Source.Chart))

	cmd := exec.Command(
		"helm",
		"template",
		fmt.Sprintf("%s/charts/%s/%s", tmpDir, a.Type, a.App.Spec.Source.Chart),
		"--output-dir", fmt.Sprintf("%s/templates/%s", tmpDir, a.Type),
		"--values", fmt.Sprintf("%s/charts/%s/%s/values.yaml", tmpDir, a.Type, a.App.Spec.Source.Chart),
		"--values", fmt.Sprintf("%s/values-%s.yaml", tmpDir, a.Type),
	)

	cmd.Stderr = os.Stderr

	err := cmd.Run()

	if err != nil {
		fmt.Println(err)
	}
}
