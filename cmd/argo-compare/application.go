package main

import (
	"errors"
	"fmt"
	"github.com/mattn/go-zglob"
	"github.com/op/go-logging"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"strings"

	h "github.com/shini4i/argo-compare/internal/helpers"
	m "github.com/shini4i/argo-compare/internal/models"
)

type Application struct {
	File          string
	Type          string // src or dst version
	App           m.Application
	chartLocation string
}

func (a *Application) parse() error {
	app := m.Application{}

	var file string

	// if we are working with a temporary file, we don't need to prepend the repo root path
	if !strings.Contains(a.File, "/tmp/") {
		file = fmt.Sprintf("%s/%s", repo.getRepoRoot(exec.Command), a.File)
	} else {
		file = a.File
	}

	log.Debugf("Parsing %s...", file)

	yamlFile := h.ReadFile(file)

	if err := yaml.Unmarshal(yamlFile, &app); err != nil {
		return err
	}

	a.App = app

	return nil
}

func (a *Application) writeValuesYaml() {
	yamlFile, err := os.Create(fmt.Sprintf("%s/values-%s.yaml", tmpDir, a.Type))
	if err != nil {
		panic(err)
	}

	if _, err := yamlFile.WriteString(a.App.Spec.Source.Helm.Values); err != nil {
		log.Fatal(err.Error())
	}
}

func (a *Application) collectHelmChart() error {
	a.chartLocation = fmt.Sprintf("%s/%s", cacheDir, a.App.Spec.Source.RepoURL)

	if err := os.MkdirAll(a.chartLocation, os.ModePerm); err != nil {
		log.Fatal(err)
	}

	// A bit hacky, but we need to support cases when helm chart tgz filename does not follow the standard naming convention
	// For example, sonarqube-4.0.0+315.tgz
	chartFileName, err := zglob.Glob(fmt.Sprintf("%s/%s-%s*.tgz", a.chartLocation, a.App.Spec.Source.Chart, a.App.Spec.Source.TargetRevision))
	if err != nil {
		log.Fatal(err)
	}

	if len(chartFileName) == 0 {
		var username, password string

		for _, repoCred := range repoCredentials {
			if repoCred.Url == a.App.Spec.Source.RepoURL {
				username = repoCred.Username
				password = repoCred.Password
				break
			}
		}

		log.Debugf("Downloading version %s of %s chart...",
			a.App.Spec.Source.TargetRevision,
			a.App.Spec.Source.Chart)

		cmd := exec.Command(
			"helm",
			"pull",
			"--destination", a.chartLocation,
			"--username", username,
			"--password", password,
			"--repo", a.App.Spec.Source.RepoURL,
			a.App.Spec.Source.Chart,
			"--version", a.App.Spec.Source.TargetRevision)

		cmd.Stdout = os.Stdout
		if logging.GetLevel(loggerName) == logging.DEBUG {
			cmd.Stderr = os.Stderr
		}

		if err := cmd.Run(); err != nil {
			return errors.New("error downloading chart")
		}
	} else {
		log.Debugf("Version %s of %s chart already downloaded...",
			a.App.Spec.Source.TargetRevision,
			a.App.Spec.Source.Chart)
	}

	return nil
}

func (a *Application) extractChart() {
	// We have a separate function for this and not using helm to extract the content of the chart
	// because we don't want to re-download the chart if the TargetRevision is the same
	log.Debugf("Extracting %s chart to %s/charts/%s...", a.App.Spec.Source.Chart, tmpDir, a.Type)

	path := fmt.Sprintf("%s/charts/%s/%s", tmpDir, a.Type, a.App.Spec.Source.Chart)
	if err := os.MkdirAll(path, os.ModePerm); err != nil {
		log.Fatal(err)
	}

	searchPattern := fmt.Sprintf("%s/%s-%s*.tgz",
		a.chartLocation,
		a.App.Spec.Source.Chart,
		a.App.Spec.Source.TargetRevision)

	chartFileName, err := zglob.Glob(searchPattern)
	if err != nil {
		log.Fatal(err)
	}

	// It's highly unlikely that we will have more than one file matching the pattern
	// Nevertheless we need to handle this case, please submit an issue if you encounter this
	if len(chartFileName) > 1 {
		log.Fatal("More than one chart file found, please check your cache directory")
	}

	cmd := exec.Command(
		"tar",
		"xf",
		chartFileName[0],
		"-C", fmt.Sprintf("%s/charts/%s", tmpDir, a.Type),
	)

	cmd.Stdout = os.Stdout
	if logging.GetLevel(loggerName) == logging.DEBUG {
		cmd.Stderr = os.Stderr
	}

	if err = cmd.Run(); err != nil {
		log.Fatal(err)
	}
}

func (a *Application) renderTemplate() {
	log.Debugf("Rendering %s template...", a.App.Spec.Source.Chart)

	cmd := exec.Command(
		"helm",
		"template",
		fmt.Sprintf("%s/charts/%s/%s", tmpDir, a.Type, a.App.Spec.Source.Chart),
		"--output-dir", fmt.Sprintf("%s/templates/%s", tmpDir, a.Type),
		"--values", fmt.Sprintf("%s/charts/%s/%s/values.yaml", tmpDir, a.Type, a.App.Spec.Source.Chart),
		"--values", fmt.Sprintf("%s/values-%s.yaml", tmpDir, a.Type),
	)

	if logging.GetLevel(loggerName) == logging.DEBUG {
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
}
