package main

import (
	"fmt"
	"github.com/mattn/go-zglob"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"strings"

	h "github.com/shini4i/argo-compare/internal/helpers"
	m "github.com/shini4i/argo-compare/internal/models"
)

type Target struct {
	CmdRunner     utils.CmdRunner
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

	if err := yaml.Unmarshal(h.ReadFile(file), &app); err != nil {
		return err
	}

	if err := app.Validate(); err != nil {
		return err
	}

	t.App = app

	return nil
}

func (t *Target) generateValuesFiles() {
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			generateValuesFile(source.Chart, tmpDir, t.Type, source.Helm.Values)
		}
	} else {
		generateValuesFile(t.App.Spec.Source.Chart, tmpDir, t.Type, t.App.Spec.Source.Helm.Values)
	}
}

func (t *Target) ensureHelmCharts() error {
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			if err := downloadHelmChart(t.CmdRunner, cacheDir, source.RepoURL, source.Chart, source.TargetRevision); err != nil {
				return err
			}
		}
	} else {
		if err := downloadHelmChart(t.CmdRunner, cacheDir, t.App.Spec.Source.RepoURL, t.App.Spec.Source.Chart, t.App.Spec.Source.TargetRevision); err != nil {
			return err
		}
	}

	return nil
}

func (t *Target) extractCharts() {
	// We have a separate function for this and not using helm to extract the content of the chart
	// because we don't want to re-download the chart if the TargetRevision is the same
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			extractHelmChart(t.CmdRunner, source.Chart, source.TargetRevision, fmt.Sprintf("%s/%s", cacheDir, source.RepoURL), tmpDir, t.Type)
		}
	} else {
		extractHelmChart(t.CmdRunner, t.App.Spec.Source.Chart, t.App.Spec.Source.TargetRevision, fmt.Sprintf("%s/%s", cacheDir, t.App.Spec.Source.RepoURL), tmpDir, t.Type)
	}
}

func (t *Target) renderAppSources() {
	var releaseName string

	// We are providing release name to the helm template command to cover some corner cases
	// when the chart is using the release name in the templates
	if !t.App.Spec.MultiSource {
		if t.App.Spec.Source.Helm.ReleaseName != "" {
			releaseName = t.App.Spec.Source.Helm.ReleaseName
		} else {
			releaseName = t.App.Metadata.Name
		}
	}

	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			if source.Helm.ReleaseName != "" {
				releaseName = source.Helm.ReleaseName
			} else {
				releaseName = t.App.Metadata.Name
			}
			if err := renderAppSource(releaseName, source.Chart, source.TargetRevision, tmpDir, t.Type); err != nil {
				log.Fatal(err)
			}
		}
		return
	}

	if err := renderAppSource(releaseName, t.App.Spec.Source.Chart, t.App.Spec.Source.TargetRevision, tmpDir, t.Type); err != nil {
		log.Fatal(err)
	}
}

func renderAppSource(releaseName, chartName, chartVersion, tmpDir, targetType string) error {
	log.Debugf("Rendering [%s] chart's version [%s] templates using release name [%s]",
		cyan(chartName),
		cyan(chartVersion),
		cyan(releaseName))

	cmd := exec.Command(
		"helm",
		"template",
		"--release-name", releaseName,
		fmt.Sprintf("%s/charts/%s/%s", tmpDir, targetType, chartName),
		"--output-dir", fmt.Sprintf("%s/templates/%s", tmpDir, targetType),
		"--values", fmt.Sprintf("%s/charts/%s/%s/values.yaml", tmpDir, targetType, chartName),
		"--values", fmt.Sprintf("%s/%s-values-%s.yaml", tmpDir, chartName, targetType),
	)

	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func generateValuesFile(chartName, tmpDir, targetType, values string) {
	yamlFile, err := os.Create(fmt.Sprintf("%s/%s-values-%s.yaml", tmpDir, chartName, targetType))
	if err != nil {
		log.Fatal(err)
	}

	if _, err := yamlFile.WriteString(values); err != nil {
		log.Fatal(err)
	}
}

func downloadHelmChart(cmdRunner utils.CmdRunner, cacheDir, repoUrl, chartName, targetRevision string) error {
	chartLocation := fmt.Sprintf("%s/%s", cacheDir, repoUrl)

	if err := os.MkdirAll(chartLocation, os.ModePerm); err != nil {
		log.Fatal(err)
	}

	// A bit hacky, but we need to support cases when helm chart tgz filename does not follow the standard naming convention
	// For example, sonarqube-4.0.0+315.tgz
	chartFileName, err := zglob.Glob(fmt.Sprintf("%s/%s-%s*.tgz", chartLocation, chartName, targetRevision))
	if err != nil {
		log.Fatal(err)
	}

	if len(chartFileName) == 0 {
		var username, password string

		for _, repoCred := range repoCredentials {
			if repoCred.Url == repoUrl {
				username = repoCred.Username
				password = repoCred.Password
				break
			}
		}

		log.Debugf("Downloading version [%s] of [%s] chart...",
			cyan(targetRevision),
			cyan(chartName))

		stdout, stderr, err := cmdRunner.Run("helm",
			"pull",
			"--destination", chartLocation,
			"--username", username,
			"--password", password,
			"--repo", repoUrl,
			chartName,
			"--version", targetRevision)

		log.Info(stdout)

		if len(stderr) > 0 {
			log.Error(stderr)
		}

		if err != nil {
			return failedToDownloadChart
		}
	} else {
		log.Debugf("Version [%s] of [%s] chart is present in the cache...",
			cyan(targetRevision),
			cyan(chartName))
	}

	return nil
}

func extractHelmChart(cmdRunner utils.CmdRunner, chartName, chartVersion, chartLocation, tmpDir, targetType string) {
	log.Debugf("Extracting [%s] chart version [%s] to %s/charts/%s...",
		cyan(chartName),
		cyan(chartVersion),
		tmpDir, targetType)

	path := fmt.Sprintf("%s/charts/%s/%s", tmpDir, targetType, chartName)
	if err := os.MkdirAll(path, os.ModePerm); err != nil {
		log.Fatal(err)
	}

	searchPattern := fmt.Sprintf("%s/%s-%s*.tgz",
		chartLocation,
		chartName,
		chartVersion)

	chartFileName, err := zglob.Glob(searchPattern)
	if err != nil {
		log.Fatal(err)
	}

	// It's highly unlikely that we will have more than one file matching the pattern
	// Nevertheless we need to handle this case, please submit an issue if you encounter this
	if len(chartFileName) > 1 {
		log.Fatal("More than one chart file found, please check your cache directory")
	}

	stdout, stderr, err := cmdRunner.Run("tar",
		"xf",
		chartFileName[0],
		"-C", fmt.Sprintf("%s/charts/%s", tmpDir, targetType),
	)

	log.Info(stdout)

	if len(stderr) > 0 {
		log.Error(stderr)
	}

	if err != nil {
		log.Fatal(err)
	}
}
