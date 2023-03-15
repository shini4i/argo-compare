package main

import (
	"errors"
	"fmt"
	"github.com/mattn/go-zglob"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"strings"

	h "github.com/shini4i/argo-compare/internal/helpers"
	m "github.com/shini4i/argo-compare/internal/models"
)

type Target struct {
	File          string
	Type          string // src or dst version
	App           m.Application
	chartLocation string
}

func (t *Target) parse() error {
	app := m.Application{}

	var file string

	// if we are working with a temporary file, we don't need to prepend the repo root path
	if !strings.Contains(t.File, "/tmp/") {
		file = fmt.Sprintf("%s/%s", h.GetGitRepoRoot(), t.File)
	} else {
		file = t.File
	}

	log.Debugf("Parsing %s...", file)

	yamlFile := h.ReadFile(file)

	if err := yaml.Unmarshal(yamlFile, &app); err != nil {
		return err
	}

	t.App = app

	return nil
}

func (t *Target) writeValuesYaml() {
	yamlFile, err := os.Create(fmt.Sprintf("%s/values-%s.yaml", tmpDir, t.Type))
	if err != nil {
		log.Fatal(err)
	}

	if _, err := yamlFile.WriteString(t.App.Spec.Source.Helm.Values); err != nil {
		log.Fatal(err)
	}
}

func (t *Target) collectHelmChart() error {
	t.chartLocation = fmt.Sprintf("%s/%s", cacheDir, t.App.Spec.Source.RepoURL)

	if err := os.MkdirAll(t.chartLocation, os.ModePerm); err != nil {
		log.Fatal(err)
	}

	// A bit hacky, but we need to support cases when helm chart tgz filename does not follow the standard naming convention
	// For example, sonarqube-4.0.0+315.tgz
	chartFileName, err := zglob.Glob(fmt.Sprintf("%s/%s-%s*.tgz", t.chartLocation, t.App.Spec.Source.Chart, t.App.Spec.Source.TargetRevision))
	if err != nil {
		log.Fatal(err)
	}

	if len(chartFileName) == 0 {
		var username, password string

		for _, repoCred := range repoCredentials {
			if repoCred.Url == t.App.Spec.Source.RepoURL {
				username = repoCred.Username
				password = repoCred.Password
				break
			}
		}

		log.Debugf("Downloading version [%s] of [%s] chart...",
			cyan(t.App.Spec.Source.TargetRevision),
			cyan(t.App.Spec.Source.Chart))

		cmd := exec.Command(
			"helm",
			"pull",
			"--destination", t.chartLocation,
			"--username", username,
			"--password", password,
			"--repo", t.App.Spec.Source.RepoURL,
			t.App.Spec.Source.Chart,
			"--version", t.App.Spec.Source.TargetRevision)

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return errors.New("error downloading chart")
		}
	} else {
		log.Debugf("Version [%s] of [%s] chart is present in the cache...",
			cyan(t.App.Spec.Source.TargetRevision),
			cyan(t.App.Spec.Source.Chart))
	}

	return nil
}

func (t *Target) extractChart() {
	// We have a separate function for this and not using helm to extract the content of the chart
	// because we don't want to re-download the chart if the TargetRevision is the same
	log.Debugf("Extracting [%s] chart to %s/charts/%s...", cyan(t.App.Spec.Source.Chart), tmpDir, t.Type)

	path := fmt.Sprintf("%s/charts/%s/%s", tmpDir, t.Type, t.App.Spec.Source.Chart)
	if err := os.MkdirAll(path, os.ModePerm); err != nil {
		log.Fatal(err)
	}

	searchPattern := fmt.Sprintf("%s/%s-%s*.tgz",
		t.chartLocation,
		t.App.Spec.Source.Chart,
		t.App.Spec.Source.TargetRevision)

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
		"-C", fmt.Sprintf("%s/charts/%s", tmpDir, t.Type),
	)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err = cmd.Run(); err != nil {
		log.Fatal(err)
	}
}

func (t *Target) renderTemplate() {
	var releaseName string

	// We are providing release name to the helm template command to cover some corner cases
	// when the chart is using the release name in the templates
	if t.App.Spec.Source.Helm.ReleaseName != "" {
		releaseName = t.App.Spec.Source.Helm.ReleaseName
	} else {
		releaseName = t.App.Metadata.Name
	}

	log.Debugf("Rendering [%s] chart's version [%s] templates using release name [%s]",
		cyan(t.App.Spec.Source.Chart),
		cyan(t.App.Spec.Source.TargetRevision),
		cyan(releaseName))

	cmd := exec.Command(
		"helm",
		"template",
		"--release-name", releaseName,
		fmt.Sprintf("%s/charts/%s/%s", tmpDir, t.Type, t.App.Spec.Source.Chart),
		"--output-dir", fmt.Sprintf("%s/templates/%s", tmpDir, t.Type),
		"--values", fmt.Sprintf("%s/charts/%s/%s/values.yaml", tmpDir, t.Type, t.App.Spec.Source.Chart),
		"--values", fmt.Sprintf("%s/values-%s.yaml", tmpDir, t.Type),
	)

	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
}
